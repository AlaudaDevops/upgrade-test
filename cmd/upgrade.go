// cmd/upgrade.go

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/AlaudaDevops/tools-upgrade-test/pkg/exec"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/AlaudaDevops/tools-upgrade-test/pkg/config"
	upctx "github.com/AlaudaDevops/tools-upgrade-test/pkg/context"
	"github.com/AlaudaDevops/tools-upgrade-test/pkg/git"
	"github.com/AlaudaDevops/tools-upgrade-test/pkg/operator"
)

var (
	configFile string
	kubeconfig string
	logLevel   string
	logger     *zap.Logger
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade",
	Short: "Run upgrade tests",
	Long: `Run upgrade tests for operators.
It will process the upgrade paths defined in the configuration file.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runUpgrade()
	},
}

func init() {
	rootCmd.AddCommand(upgradeCmd)

	// Add flags
	upgradeCmd.Flags().StringVar(&configFile, "config", "config.yaml", "path to configuration file")
	upgradeCmd.Flags().StringVar(&kubeconfig, "kubeconfig", "", "path to kubeconfig file, if not set, get KUBECONFIG from env, or ~/.kube/config")
	upgradeCmd.Flags().StringVar(&logLevel, "log-level", "info", "log level (debug, info, warn, error)")
}

func getKubeconfig() string {
	if kubeconfig == "" {
		kubeconfig = os.Getenv("KUBECONFIG")
		if kubeconfig == "" {
			kubeconfig = filepath.Join(os.Getenv("HOME"), ".kube", "config")
		}
	}
	return kubeconfig
}

// NewLogger creates a new logger with the given level and options
func NewLogger(level string, opts ...zap.Option) (*zap.Logger, error) {
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

func runUpgrade() error {
	kubeconfig := getKubeconfig()

	// Load configuration
	cfg, err := config.LoadConfig(configFile)
	if err != nil {
		return fmt.Errorf("failed to load config: %v", err)
	}

	// Create logger with configured level
	logger, err = NewLogger(cfg.LogLevel)
	if err != nil {
		return fmt.Errorf("failed to create logger: %v", err)
	}
	defer logger.Sync()

	// Create context with logger
	ctx := upctx.WithOperatorNamespace(context.Background(), cfg.Operator.OperatorNamespace)
	ctx = upctx.WithSystemNamespace(ctx, cfg.Operator.SystemNamespace)
	ctx = upctx.WithMaxRetries(ctx, cfg.Operator.MaxRetries)
	ctx = upctx.WithLogger(ctx, logger.Sugar())

	// Load kubernetes configuration
	k8sConfig, err := loadKubeConfig(kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load kubernetes config: %v", err)
	}

	// Create operator manager
	op, err := operator.NewOperator(k8sConfig)
	if err != nil {
		return fmt.Errorf("failed to create operator manager: %v", err)
	}

	for _, path := range cfg.UpgradePaths {
		if err := processUpgrade(ctx, op, path, cfg); err != nil {
			return fmt.Errorf("failed to process upgrade path: %v", err)
		}
	}

	logger.Info("upgrade test completed successfully")
	return nil
}

func processUpgrade(ctx context.Context, op *operator.Operator, path config.UpgradePath, cfg *config.Config) error {
	logger := upctx.LoggerFromContext(ctx)
	logger = logger.Named(path.Name)
	ctx = upctx.WithLogger(ctx, logger)

	// Create git manager
	gitManager, err := git.NewGitManager(
		filepath.Join(cfg.Workspace, path.Name),
		cfg.Git.Repository,
		cfg.Git.Username,
		cfg.Git.Password,
	)
	if err != nil {
		return fmt.Errorf("failed to create git manager: %v", err)
	}

	for _, version := range path.Versions {
		var cloneDir string
		var err error
		if cfg.Git.Repository != "" && version.Git.Revision != "" && version.BuildCommand != "" {
			// Clone and build from git
			cloneDir, err = gitManager.Clone(ctx, version.Name, &version.Git)
			if err != nil {
				return fmt.Errorf("failed to process git version: %v", err)
			}
		}

		if version.BuildCommand != "" {
			// Build operator
			err := gitManager.Build(ctx, cloneDir, version.BuildCommand)
			if err != nil {
				return fmt.Errorf("failed to build operator: %v", err)
			}
		}

		// Deploy operator
		if err := deployOperator(ctx, op, cfg.Operator.ArtifactName, version); err != nil {
			return fmt.Errorf("failed to deploy operator: %v", err)
		}

		// Execute upgrade test
		if err := execUpgradeTest(ctx, filepath.Join(cloneDir, version.TestSubPath), version.TestCommand); err != nil {
			return fmt.Errorf("failed to execute upgrade test: %v", err)
		}

		// Cleanup
		if cfg.Cleanup {
			defer gitManager.Cleanup(cloneDir)
		}
	}

	return nil
}

func loadKubeConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	return rest.InClusterConfig()
}

func deployOperator(ctx context.Context, op *operator.Operator, artifactName string, version config.Version) error {
	operatorName, startCSVName, err := op.PrepareOperator(ctx, artifactName, version)
	if err != nil {
		return fmt.Errorf("failed to prepare operator: %v", err)
	}

	return op.InstallOperator(ctx, operatorName, startCSVName)
}

func execUpgradeTest(ctx context.Context, workDir, command string) error {
	logger := upctx.LoggerFromContext(ctx)
	logger.Infow("executing upgrade test", "command", command)

	result := exec.RunCommand(ctx, exec.Command{
		Name: "bash",
		Args: []string{"-c", command},
		Dir:  "testing/gitlab",
	})

	if result.Err != nil {
		return fmt.Errorf("failed to execute upgrade test: %v", result.Err)
	}

	return nil
}
