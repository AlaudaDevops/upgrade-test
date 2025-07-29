// pkg/git/git.go

package git

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/AlaudaDevops/tools-upgrade-test/pkg/config"
	upctx "github.com/AlaudaDevops/tools-upgrade-test/pkg/context"
	"github.com/AlaudaDevops/tools-upgrade-test/pkg/exec"
	"go.uber.org/zap"
)

// GitManager handles git operations
type GitManager struct {
	// Base directory for git operations
	baseDir string
	// Repository URL
	repoURL string
	// Username for git authentication
	username string
	// Password for git authentication
	password string
}

// sanitizePath ensures directory names only contain allowed characters (0-9, A-Z, a-z, _, -, .)
// and replaces multiple consecutive underscores with a single one
// Parameters:
//   - path: The path to sanitize
//
// Returns:
//   - string: The sanitized path
func sanitizePath(path string) string {
	// Split the path into parts
	parts := strings.Split(path, string(os.PathSeparator))

	// Process each part
	for i, part := range parts {
		// Replace all non-allowed characters with underscore
		parts[i] = strings.Map(func(r rune) rune {
			// Allow alphanumeric characters, underscore, hyphen and dot
			if (r >= '0' && r <= '9') || // numbers
				(r >= 'A' && r <= 'Z') || // uppercase letters
				(r >= 'a' && r <= 'z') || // lowercase letters
				r == '_' || r == '.' { // special characters
				return r
			}
			return '_'
		}, part)

		// Replace multiple consecutive underscores with a single one
		parts[i] = regexp.MustCompile(`_+`).ReplaceAllString(parts[i], "_")
	}

	// Join the parts back together
	return strings.Join(parts, string(os.PathSeparator))
}

// NewGitManager creates a new GitManager instance
func NewGitManager(baseDir, repoURL, username, password string) (*GitManager, error) {
	// Sanitize the base directory path
	sanitizedBaseDir := sanitizePath(baseDir)

	// Create base directory if it doesn't exist
	if err := os.MkdirAll(sanitizedBaseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create base directory: %v", err)
	}

	return &GitManager{
		baseDir:  sanitizedBaseDir,
		repoURL:  repoURL,
		username: username,
		password: password,
	}, nil
}

// CloneResult is the result of a clone operation
type CloneResult struct {
	// Path to the cloned repository
	RepoPath string
	// Bundle image that was built
	BundleImage string
	// Operator image that was built
	OperatorImage string
}

// CloneAndBuild clones the repository and builds the operator
func (g *GitManager) Clone(ctx context.Context, version string, gitConfig *config.GitConfig) (string, error) {
	// Create a unique directory for this clone
	cloneDir := filepath.Join(g.baseDir, version)
	// If the cloneDir already exists, remove it and recreate to ensure a clean environment
	if _, err := os.Stat(cloneDir); err == nil {
		// Remove the existing directory and its contents
		if removeErr := os.RemoveAll(cloneDir); removeErr != nil {
			return "", fmt.Errorf("failed to remove existing clone directory: %v", removeErr)
		}
	}

	if err := os.MkdirAll(cloneDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create clone directory: %v", err)
	}

	// Clone the repository
	if err := g.cloneRepository(ctx, cloneDir, gitConfig.Revision); err != nil {
		return "", fmt.Errorf("failed to clone repository: %v", err)
	}

	return cloneDir, nil
}

// Build builds the operator
func (g *GitManager) Build(ctx context.Context, cloneDir string, buildCommand string) error {
	if err := g.buildOperator(ctx, cloneDir, buildCommand); err != nil {
		return fmt.Errorf("failed to build operator: %v", err)
	}

	return nil
}

// cloneRepository clones the repository to the specified directory

func (g *GitManager) cloneRepository(ctx context.Context, targetDir, revision string) error {
	logger := upctx.LoggerFromContext(ctx)
	logger.Info("starting git repository clone",
		zap.String("targetDir", targetDir),
		zap.String("revision", revision),
		zap.String("repository", g.repoURL))

	// Create a temporary directory for cloning to avoid conflicts and ensure isolation
	tempDir, err := os.MkdirTemp("", "git-clone-*")
	if err != nil {
		logger.Error("failed to create temporary directory",
			zap.Error(err))
		return fmt.Errorf("failed to create temporary directory for git clone: %v", err)
	}
	// Clean up the temporary directory after use
	defer func() {
		if err := os.RemoveAll(tempDir); err != nil {
			logger.Warn("failed to cleanup temporary directory",
				zap.String("tempDir", tempDir),
				zap.Error(err))
		}
	}()

	logger.Debug("initializing git repository",
		zap.String("tempDir", tempDir),
		zap.String("revision", revision))
	initResult := exec.RunCommand(ctx, exec.Command{Name: "git", Args: []string{"init"}, Dir: tempDir})
	if initResult.Err != nil {
		logger.Error("failed to initialize git repository",
			zap.String("tempDir", tempDir),
			zap.Error(initResult.Err))
		return fmt.Errorf("failed to initialize git repository: %v", initResult.Err)
	}

	// Configure git credentials if provided
	if g.username != "" && g.password != "" {
		logger.Debug("configuring git credentials")
		// Set credential helper to store credentials
		credentialResult := exec.RunCommand(ctx, exec.Command{
			Name: "git",
			Args: []string{"config", "credential.helper", "store"},
			Dir:  tempDir,
		})
		if credentialResult.Err != nil {
			logger.Error("failed to configure git credentials",
				zap.Error(credentialResult.Err))
			return fmt.Errorf("failed to configure git credentials: %v", credentialResult.Err)
		}

		gitUrl, err := url.Parse(g.repoURL)
		if err != nil {
			logger.Error("failed to parse git URL",
				zap.String("repoURL", g.repoURL),
				zap.Error(err))
			return fmt.Errorf("failed to parse git URL: %v", err)
		}
		gitUrl.User = url.UserPassword(g.username, g.password)
		g.repoURL = gitUrl.String()
	}

	// Add remote
	logger.Debug("adding remote",
		zap.String("repoURL", g.repoURL))
	remoteResult := exec.RunCommand(ctx, exec.Command{Name: "git", Args: []string{"remote", "add", "origin", g.repoURL}, Dir: tempDir})
	if remoteResult.Err != nil {
		logger.Error("failed to add remote",
			zap.String("repoURL", g.repoURL),
			zap.Error(remoteResult.Err))
		return fmt.Errorf("failed to add remote: %v", remoteResult.Err)
	}

	// Fetch the specified revision
	logger.Debug("fetching revision",
		zap.String("revision", revision))
	fetchResult := exec.RunCommand(ctx, exec.Command{Name: "git", Args: []string{"fetch", "origin", revision}, Dir: tempDir})
	if fetchResult.Err != nil {
		logger.Error("failed to fetch revision",
			zap.String("revision", revision),
			zap.Error(fetchResult.Err))
		return fmt.Errorf("failed to fetch revision: %v", fetchResult.Err)
	}

	// Checkout the revision
	logger.Debug("checking out revision",
		zap.String("revision", revision))
	checkoutResult := exec.RunCommand(ctx, exec.Command{Name: "git", Args: []string{"checkout", "FETCH_HEAD"}, Dir: tempDir})
	if checkoutResult.Err != nil {
		logger.Error("failed to checkout revision",
			zap.String("revision", revision),
			zap.Error(checkoutResult.Err))
		return fmt.Errorf("failed to checkout revision: %v", checkoutResult.Err)
	}

	// Copy the repository to the target directory
	logger.Debug("copying repository to target directory",
		zap.String("targetDir", targetDir))
	copyResult := exec.RunCommand(ctx, exec.Command{Name: "cp", Args: []string{"-rf", tempDir + "/", targetDir}, Dir: g.baseDir})
	if copyResult.Err != nil {
		logger.Error("failed to copy repository to target directory",
			zap.String("targetDir", targetDir),
			zap.Error(copyResult.Err))
		return fmt.Errorf("failed to copy repository to target directory: %v", copyResult.Err)
	}

	logger.Info("successfully cloned repository",
		zap.String("targetDir", targetDir),
		zap.String("revision", revision))
	return nil
}

// buildOperator builds the operator using the specified build command
func (g *GitManager) buildOperator(ctx context.Context, repoPath string, buildCommand string) error {
	logger := upctx.LoggerFromContext(ctx)
	logger.Info("building operator",
		zap.String("repoPath", repoPath),
		zap.String("buildCommand", buildCommand))

	// Execute the build command
	buildResult := exec.RunCommand(ctx, exec.Command{Name: "sh", Args: []string{"-c", buildCommand}, Dir: repoPath})
	if buildResult.Err != nil {
		logger.Error("failed to execute build command",
			zap.String("buildCommand", buildCommand),
			zap.Error(buildResult.Err))
		return fmt.Errorf("failed to execute build command: %v", buildResult.Err)
	}

	logger.Info("successfully built operator",
		zap.String("repoPath", repoPath))
	return nil
}

// Cleanup removes the cloned repository
func (g *GitManager) Cleanup(repoPath string) error {
	logger := zap.L()
	logger.Info("cleaning up repository",
		zap.String("repoPath", repoPath))

	if err := os.RemoveAll(repoPath); err != nil {
		logger.Error("failed to cleanup repository",
			zap.String("repoPath", repoPath),
			zap.Error(err))
		return err
	}

	logger.Info("successfully cleaned up repository",
		zap.String("repoPath", repoPath))
	return nil
}
