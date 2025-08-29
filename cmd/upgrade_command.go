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
	configFile string
	kubeconfig string
	logLevel   string
	workspace  string
	logger     *zap.Logger
	config     *config.Config
	operator   operator.OperatorInterface
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
