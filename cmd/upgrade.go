// cmd/upgrade.go

package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/AlaudaDevops/tools-upgrade-test/pkg/config"
	upctx "github.com/AlaudaDevops/tools-upgrade-test/pkg/context"
	"github.com/AlaudaDevops/tools-upgrade-test/pkg/exec"
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
	ctx := upctx.WithLogger(context.Background(), logger.Sugar())

	// Load kubernetes configuration
	k8sConfig, err := loadKubeConfig(kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load kubernetes config: %v", err)
	}

	// Create operator manager
	op, err := operator.NewOperator(k8sConfig, cfg.OperatorNamespace, cfg.OperatorName)
	if err != nil {
		return fmt.Errorf("failed to create operator manager: %v", err)
	}

	for _, path := range cfg.UpgradePaths {
		if err := processUpgrade(ctx, op, path, cfg); err != nil {
			if !cfg.Immediate {
				logger.Error("failed to process upgrade path", zap.String("path", path.Name), zap.Error(err))
				continue
			}
			return fmt.Errorf("failed to process upgrade path: %s, error: %v", path.Name, err)
		}
	}

	logger.Info("upgrade test completed successfully")
	return nil
}

func processUpgrade(ctx context.Context, op *operator.Operator, path config.UpgradePath, cfg *config.Config) error {
	logger := upctx.LoggerFromContext(ctx)
	logger.Infow("==> processing upgrade path", "path", path.Name)
	for index, version := range path.Versions {
		logger.Infow("deploying operator version", "version", version.Name)
		av, err := op.InstallArtifactVersion(ctx, version.BundleVersion)
		if err != nil {
			return fmt.Errorf("failed to prepare operator: %v", err)
		}

		csv, _, _ := unstructured.NestedString(av.Object, "status", "version")
		if err := op.InstallSubscription(ctx, csv); err != nil {
			return fmt.Errorf("failed to install subscription: %v", err)
		}

		testCommand := "make upgrade"
		if index == 0 {
			testCommand = "make prepare"
		}
		if version.TestCommand != "" {
			testCommand = version.TestCommand
		}

		if err := execCommand(ctx, cfg.Workspace, fmt.Sprintf("git checkout %s", version.Revision)); err != nil {
			return fmt.Errorf("failed to checkout revision %s: %v", version.Revision, err)
		}

		if err := execCommand(ctx,
			fmt.Sprintf("%s/%s", cfg.Workspace, version.TestSubPath),
			fmt.Sprintf("%s && %s", cfg.PrepareCommand, testCommand)); err != nil {
			return fmt.Errorf("failed to execute test command: %v", err)
		}
		logger.Info("upgrade test passed", "version", version.Name)
	}
	logger.Infow("==> upgrade path completed", "path", path.Name)

	return nil
}

func loadKubeConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	return rest.InClusterConfig()
}

func execCommand(ctx context.Context, workDir, command string) error {
	logger := upctx.LoggerFromContext(ctx)
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
