package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/AlaudaDevops/upgrade-test/pkg/config"
	"github.com/AlaudaDevops/upgrade-test/pkg/exec"
	"github.com/AlaudaDevops/upgrade-test/pkg/operator"
	"knative.dev/pkg/logging"
)

// UpgradeCommand represents the upgrade command implementation
type UpgradeCommand struct {
	configFile     string
	kubeconfig     string
	logLevel       string
	workspace      string
	skipPreflight  bool
	confirmCluster string
	logger         *zap.Logger
	config         *config.Config
	operator       operator.OperatorInterface
}

// NewUpgradeCommand creates a new instance of UpgradeCommand
func NewUpgradeCommand() *UpgradeCommand {
	return &UpgradeCommand{}
}

// NewUpgradeCommandWithDeps creates a new instance of UpgradeCommand with dependencies
func NewUpgradeCommandWithDeps(operator operator.OperatorInterface, config *config.Config) *UpgradeCommand {
	return &UpgradeCommand{
		operator: operator,
		config:   config,
	}
}

// SetOperator sets the operator instance (useful for testing)
func (uc *UpgradeCommand) SetOperator(operator operator.OperatorInterface) {
	uc.operator = operator
}

// SetConfig sets the configuration (useful for testing)
func (uc *UpgradeCommand) SetConfig(config *config.Config) {
	uc.config = config
}

// GetOperator returns the current operator instance
func (uc *UpgradeCommand) GetOperator() operator.OperatorInterface {
	return uc.operator
}

// GetConfig returns the current configuration
func (uc *UpgradeCommand) GetConfig() *config.Config {
	return uc.config
}

// AddFlags adds command line flags to the upgrade command
func (uc *UpgradeCommand) AddFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&uc.configFile, "config", "config.yaml", "path to configuration file")
	cmd.Flags().StringVar(&uc.kubeconfig, "kubeconfig", "", "path to kubeconfig file, if not set, get KUBECONFIG from env, or ~/.kube/config")
	cmd.Flags().StringVar(&uc.logLevel, "log-level", "info", "log level (debug, info, warn, error)")
	cmd.Flags().StringVar(&uc.workspace, "workspace", "", "workspace for the operator")
	cmd.Flags().BoolVar(&uc.skipPreflight, "skip-preflight", false, "skip the read-only preflight check that scans the target cluster for residual Subscription/ArtifactVersion/InstallPlan (NOT recommended)")
	cmd.Flags().StringVar(&uc.confirmCluster, "confirm-cluster", "", "required when operatorConfig.violet.clusters is set: must equal the KUBECONFIG current-context to prevent accidentally upgrading the wrong cluster")

	// best-practices #3-C: cobra's default is to dump --help on every RunE
	// error, which would push the actionable kubectl-delete lines from
	// PreflightError off the screen. Silence usage here; flag-parsing errors
	// (handled before RunE) still print usage, so users typo'ing a flag are
	// not stranded.
	cmd.SilenceUsage = true
}

// Execute runs the upgrade command
func (uc *UpgradeCommand) Execute() error {
	kubeconfig := uc.getKubeconfig()

	// Load configuration
	cfg, err := config.LoadConfig(uc.configFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %v", err)
	}
	uc.config = cfg

	if uc.workspace != "" {
		cfg.OperatorConfig.Workspace = uc.workspace
	}

	// Create logger with configured level
	logger, err := uc.newLogger(cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("failed to create logger: %v", err)
	}
	uc.logger = logger
	defer logger.Sync()

	// Create context with logger
	ctx := logging.WithLogger(context.Background(), logger.Sugar())

	// Load kubernetes configuration
	k8sConfig, err := uc.loadKubeConfig(kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load kubernetes config: %v", err)
	}

	logger.Info("operator type", zap.String("type", cfg.OperatorConfig.Type))

	// Cluster identity guard: when violet.clusters is configured, the user
	// is opting into a multi-cluster ACP topology where kubectl and violet
	// could silently point at different clusters. Force them to confirm
	// the KUBECONFIG context name so a missing/typo'd --confirm-cluster
	// surfaces as a hard error rather than "preflight clean / upgrade
	// writes to prod".
	if err := uc.assertClusterMatch(cfg, kubeconfig); err != nil {
		return err
	}

	// Create operator manager using factory
	factory := operator.NewOperatorFactory()
	op, err := factory.CreateOperator(operator.OperatorType(cfg.OperatorConfig.Type), operator.OperatorOptions{
		Config:         k8sConfig,
		Namespace:      cfg.OperatorConfig.Namespace,
		Name:           cfg.OperatorConfig.Name,
		OperatorConfig: cfg.OperatorConfig,
	})
	if err != nil {
		return fmt.Errorf("failed to create operator manager: %v", err)
	}
	uc.operator = op

	if len(cfg.UpgradePaths) == 0 {
		logger.Info("no upgrade paths found, skipping")
		return nil
	}

	// Preflight: read-only scan for residual OLM resources that would
	// conflict with starting an upgrade at any path's baseline. Fails fast
	// at the first dirty path so the user gets one set of cleanup commands
	// instead of an accumulated megareport.
	if uc.skipPreflight {
		logger.Warn("preflight skipped by --skip-preflight; ensure environment is clean")
	} else if err := uc.runPreflight(ctx); err != nil {
		return err
	}

	// Process upgrade paths
	for _, path := range cfg.UpgradePaths {
		if err := uc.process(ctx, path); err != nil {
			if !cfg.Immediate {
				logger.Error("failed to process upgrade path", zap.String("path", path.Name), zap.Error(err))
				continue
			}
			return fmt.Errorf("failed to process upgrade path: %s, error: %v", path.Name, err)
		}
	}
	return nil
}

// assertClusterMatch enforces the operator≡kubectl cluster identity contract
// when violet.clusters is configured. It is a no-op for configs that do not
// use violet's multi-cluster path.
//
// When violet.clusters is set the user has opted into a multi-cluster ACP
// topology where wrong-cluster writes are catastrophic, so the guard is
// strict in EVERY mode:
//
//   - File-mode kubeconfig: --confirm-cluster must equal CurrentContext.
//   - In-cluster (empty kubeconfig path): there is no context name to read,
//     so --confirm-cluster must equal violet.clusters — the user explicitly
//     acknowledges the target subcluster.
//   - Empty CurrentContext in a multi-context kubeconfig is a user
//     configuration error, not an environmental constraint. We refuse to
//     run and tell the user to `kubectl config use-context <name>` first.
//
// DECISION B (locked at default — B1 / exact equality): the matching rule
// is exact string equality. To loosen to substring/regex matching, replace
// the two comparisons below; the flag surface (--confirm-cluster) stays
// unchanged either way.
func (uc *UpgradeCommand) assertClusterMatch(cfg *config.Config, kubeconfig string) error {
	if cfg.OperatorConfig.Violet == nil || cfg.OperatorConfig.Violet.Clusters == "" {
		return nil
	}
	violetClusters := cfg.OperatorConfig.Violet.Clusters

	if kubeconfig == "" {
		// In-cluster mode: no CurrentContext to read. Require the user to
		// explicitly acknowledge the target subcluster via --confirm-cluster
		// matching violet.clusters. Without this guard a CI pod silently
		// writes to whatever cluster its serviceaccount is bound to.
		if uc.confirmCluster == "" {
			return fmt.Errorf(
				"violet.clusters=%q is set but running in-cluster (no KUBECONFIG file); "+
					"--confirm-cluster=%q is required to acknowledge the target subcluster",
				violetClusters, violetClusters,
			)
		}
		if uc.confirmCluster != violetClusters {
			return fmt.Errorf(
				"--confirm-cluster=%q does not match violet.clusters=%q; "+
					"in-cluster mode requires the two to be identical",
				uc.confirmCluster, violetClusters,
			)
		}
		return nil
	}

	apiCfg, err := clientcmd.LoadFromFile(kubeconfig)
	if err != nil {
		return fmt.Errorf("read kubeconfig %s for cluster guard: %v", kubeconfig, err)
	}
	currentCtx := apiCfg.CurrentContext
	if currentCtx == "" {
		// User configuration error: multi-context kubeconfig with no
		// current-context set. Don't silently fall back — `upgrade` would
		// otherwise inherit whatever default the client picks. Make the
		// user pick.
		return fmt.Errorf(
			"violet.clusters=%q but kubeconfig %s has no current-context; "+
				"run `kubectl config use-context <name>` first",
			violetClusters, kubeconfig,
		)
	}
	if uc.confirmCluster == "" {
		return fmt.Errorf(
			"violet.clusters=%q requires --confirm-cluster=<KUBECONFIG_CONTEXT_NAME> to confirm the target cluster (current context: %q)",
			violetClusters, currentCtx,
		)
	}
	if uc.confirmCluster != currentCtx {
		return fmt.Errorf(
			"--confirm-cluster=%q does not match KUBECONFIG current-context %q; refusing to operate on the wrong cluster",
			uc.confirmCluster, currentCtx,
		)
	}
	return nil
}

// runPreflight iterates the configured upgrade paths and calls
// op.PreflightBaseline on each baseline (Versions[0]). The first path
// reporting a non-empty residual list stops the run with a *PreflightError
// — failing fast spares the user a multi-path megareport when the first
// dirty cluster already tells them what to clean.
func (uc *UpgradeCommand) runPreflight(ctx context.Context) error {
	for _, path := range uc.config.UpgradePaths {
		if len(path.Versions) == 0 {
			continue
		}
		residuals, err := uc.operator.PreflightBaseline(ctx, path.Versions[0])
		if err != nil {
			return err
		}
		if len(residuals) > 0 {
			return &PreflightError{Residuals: residuals}
		}
	}
	return nil
}

// getKubeconfig returns the kubeconfig path
func (uc *UpgradeCommand) getKubeconfig() string {
	if uc.kubeconfig == "" {
		uc.kubeconfig = os.Getenv("KUBECONFIG")
		if uc.kubeconfig == "" {
			uc.kubeconfig = filepath.Join(os.Getenv("HOME"), ".kube", "config")
		}
	}

	// If KUBECONFIG is not set, set it to the kubeconfig path from config file, which will be inherited by the shell running test commands
	if os.Getenv("KUBECONFIG") == "" {
		os.Setenv("KUBECONFIG", uc.kubeconfig)
	}

	return uc.kubeconfig
}

// newLogger creates a new logger with the given level and options
func (uc *UpgradeCommand) newLogger(level string, opts ...zap.Option) (*zap.Logger, error) {
	// Parse log level
	var zapLevel zapcore.Level
	if err := zapLevel.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("invalid log level: %v", err)
	}

	// Create encoder config
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

	// Create console encoder
	consoleEncoder := zapcore.NewConsoleEncoder(encoderConfig)

	// Create core that writes to stdout
	core := zapcore.NewCore(
		consoleEncoder,
		zapcore.AddSync(zapcore.Lock(os.Stdout)),
		zapLevel,
	)

	// Create logger with options
	return zap.New(core, opts...), nil
}

// loadKubeConfig loads kubernetes configuration
func (uc *UpgradeCommand) loadKubeConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	return rest.InClusterConfig()
}

// processUpgrade processes a single upgrade path
func (uc *UpgradeCommand) process(ctx context.Context, path config.UpgradePath) error {
	logger := logging.FromContext(ctx)
	logger.Infow("==> processing upgrade path", "path", path.Name)

	for index, version := range path.Versions {
		logger.Infow("deploying operator version", "version", version.Name)

		// Install artifact version
		if err := uc.operator.UpgradeOperator(ctx, version); err != nil {
			return fmt.Errorf("failed to prepare operator: %v", err)
		}

		// Determine test command
		testCommand := "REPO=allure make upgrade"
		if index == 0 {
			testCommand = "REPO=allure make prepare"
		}
		if version.TestCommand != "" {
			testCommand = version.TestCommand
		}

		workspace := uc.config.OperatorConfig.Workspace
		if version.TestSubPath != "" {
			workspace = fmt.Sprintf("%s/%s", uc.config.OperatorConfig.Workspace, version.TestSubPath)
		}

		// Execute test commands
		if err := uc.execCommand(ctx,
			workspace,
			testCommand); err != nil {
			return fmt.Errorf("failed to execute test command: %v", err)
		}

		logger.Info("upgrade test passed", "version", version.Name)
	}

	logger.Infow("==> upgrade path completed", "path", path.Name)
	return nil
}

// execCommand executes a command in the given working directory
func (uc *UpgradeCommand) execCommand(ctx context.Context, workDir, command string) error {
	logger := logging.FromContext(ctx)
	logger.Infow("executing upgrade test", "command", command)

	result := exec.RunCommand(ctx, exec.Command{
		Name: "bash",
		Args: []string{"-c", command},
		Dir:  workDir,
	})

	if result.Err != nil {
		return fmt.Errorf("failed to execute upgrade test: %v", result.Err)
	}

	return nil
}
