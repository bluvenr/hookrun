package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// Templates for init command
const (
	configTemplate = `# HookRun Global Configuration
server:
  port: 9000
  route: "/webhook"
  # allow_all: false             # allow /webhook to iterate all configs (default: false)
  # max_body_size_mb: 10         # max request body in MB, 0 = unlimited (default: 10)

log:
  mode: "daily"                  # "daily" (default) | "single"
  path: "./logs"
  retention_days: 30             # only for daily mode
  # max_size_mb: 0               # only for single mode, 0 = unlimited (default)

config_dir: "./hooks"
`

	hooksTemplateGeneric = `# Hook Rule Configuration
name: "my-webhook"

# Authentication (optional)
auth:
  # TIP: For production, set token via environment variable interpolation
  token:
    source: "header"             # "header" or "query"
    key: "X-Webhook-Token"
    value: "your-secret-token"
  # hmac:
  #   header: "X-Hub-Signature-256"
  #   secret: "your-hmac-secret"   # or use env variable interpolation
  #   algorithm: "sha256"
  # ip_whitelist:
  #   - "192.168.1.0/24"

# Execution policy (applies to all rules, can be overridden per-rule)
execution:
  policy: "block"                # "block" | "always" | "cooldown"
  # cooldown_seconds: 300        # only for "cooldown" policy

# Filters: matching conditions (AND relationship)
filters:
  - type: "header"               # "header" | "query" | "body"
    key: "X-Custom-Event"
    operator: "eq"               # "eq" | "ne" | "contains" | "regex"
    value: "trigger"

rules:
  - name: "my-action"
    filters:
      - type: "body"
        key: "action"
        operator: "eq"
        value: "deploy"
    actions:
      - type: "command"
        cmd: "echo 'Hello from HookRun! Event: {{.header.X-Custom-Event}}'"
        timeout: 30
`

	hooksTemplateGitHub = `# GitHub Webhook Auto-Deploy
name: "github-auto-deploy"

# Authentication: GitHub webhook secret
# TIP: For production, set secret via environment variable interpolation
auth:
  hmac:
    header: "X-Hub-Signature-256"
    secret: "your-github-webhook-secret"
    algorithm: "sha256"
  # Restrict to GitHub webhook IP ranges (optional)
  # ip_whitelist:
  #   - "192.30.252.0/22"
  #   - "185.199.108.0/22"

# File-level filter: only handle push events
filters:
  - type: "header"
    key: "X-GitHub-Event"
    operator: "eq"
    value: "push"

execution:
  policy: "block"

rules:
  # Deploy when pushing to main branch
  - name: "push-to-main"
    filters:
      - type: "body"
        key: "ref"
        operator: "eq"
        value: "refs/heads/main"
    actions:
      - type: "command"
        cmd: "echo 'Deploying {{.body.ref}} by {{.body.pusher.name}}'"
        timeout: 30
      - type: "command"
        cmd: "git pull origin main"
        timeout: 60
      # Add your deploy commands here
      # - type: "command"
      #   cmd: "./deploy.sh"
      #   timeout: 300

  # Notify on new tag release
  - name: "tag-release"
    execution:
      policy: "always"
    filters:
      - type: "body"
        key: "ref"
        operator: "regex"
        value: "refs/tags/v.*"
    actions:
      - type: "command"
        cmd: "echo 'New release: {{.body.ref}}'"
        timeout: 30
      # Optional: send notification via webhook
      # - type: "webhook"
      #   url: "https://hooks.slack.com/services/xxx"
      #   method: "POST"
      #   body: |
      #     {"text": "New release: {{.body.ref}}"}
`

	hooksTemplateGitLab = `# GitLab Webhook Auto-Deploy
name: "gitlab-auto-deploy"

# Authentication: GitLab webhook secret token
# TIP: For production, set token via environment variable interpolation
auth:
  token:
    source: "header"
    key: "X-Gitlab-Token"
    value: "your-gitlab-webhook-token"
  # Optional: restrict to GitLab server IPs
  # ip_whitelist:
  #   - "10.0.0.0/8"

# File-level filter: only handle push events
filters:
  - type: "body"
    key: "object_kind"
    operator: "eq"
    value: "push"

execution:
  policy: "block"

rules:
  # Deploy when pushing to main branch
  - name: "push-to-main"
    filters:
      - type: "body"
        key: "ref"
        operator: "eq"
        value: "refs/heads/main"
    actions:
      - type: "command"
        cmd: "echo 'Deploying {{.body.ref}} by {{.body.user_name}}'"
        timeout: 30
      - type: "command"
        cmd: "git pull origin main"
        timeout: 60
      # Add your deploy commands here
      # - type: "command"
      #   cmd: "./deploy.sh"
      #   timeout: 300

  # Handle merge request events
  - name: "merge-request"
    filters:
      - type: "body"
        key: "object_kind"
        operator: "eq"
        value: "merge_request"
    actions:
      - type: "command"
        cmd: "echo 'MR: {{.body.object_attributes.title}} ({{.body.object_attributes.state}})'"
        timeout: 30
`
)

// builtinTemplates lists available templates
var builtinTemplates = map[string]string{
	"generic": "Generic webhook template (default)",
	"github":  "GitHub webhook auto-deploy",
	"gitlab":  "GitLab webhook auto-deploy",
}

func initCmd() *cobra.Command {
	var template string
	var force bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize a new HookRun configuration",
		Long: `Initialize a new HookRun configuration in the current directory.

Creates config.yaml and hooks/ directory with an example rule file.

Available templates:
  generic  - Generic webhook template (default)
  github   - GitHub webhook auto-deploy
  gitlab   - GitLab webhook auto-deploy`,
		Example: `  hookrun init                      # Initialize with generic template
  hookrun init --template github    # Initialize with GitHub template
  hookrun init --template gitlab    # Initialize with GitLab template
  hookrun init --force              # Overwrite existing files`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInit(template, force)
		},
	}

	cmd.Flags().StringVarP(&template, "template", "t", "generic", "Template to use (generic, github, gitlab)")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Overwrite existing files without prompting")

	return cmd
}

func runInit(template string, force bool) error {
	// Validate template name
	template = strings.ToLower(template)
	if _, ok := builtinTemplates[template]; !ok {
		return fmt.Errorf("unknown template: %q (available: generic, github, gitlab)", template)
	}

	// Check existing files
	configExists := fileExists("config.yaml")
	hooksDirExists := dirExists("hooks")

	if configExists || hooksDirExists {
		if !force {
			fmt.Println("Existing configuration detected:")
			if configExists {
				fmt.Println("  - config.yaml")
			}
			if hooksDirExists {
				fmt.Println("  - hooks/")
			}
			fmt.Println()
			fmt.Print("Overwrite? [y/N]: ")

			var answer string
			fmt.Scanln(&answer)
			answer = strings.ToLower(strings.TrimSpace(answer))
			if answer != "y" && answer != "yes" {
				fmt.Println("Aborted.")
				return nil
			}
		}
	}

	// Create hooks directory
	if err := os.MkdirAll("hooks", 0755); err != nil {
		return fmt.Errorf("failed to create hooks directory: %w", err)
	}

	// Write config.yaml
	if err := writeFileWithNotice("config.yaml", configTemplate); err != nil {
		return fmt.Errorf("failed to write config.yaml: %w", err)
	}

	// Write hooks example based on template
	var hooksContent string
	switch template {
	case "github":
		hooksContent = hooksTemplateGitHub
	case "gitlab":
		hooksContent = hooksTemplateGitLab
	default:
		hooksContent = hooksTemplateGeneric
	}

	hooksFile := filepath.Join("hooks", "example.yaml")
	if err := writeFileWithNotice(hooksFile, hooksContent); err != nil {
		return fmt.Errorf("failed to write %s: %w", hooksFile, err)
	}

	// Print summary
	fmt.Println()
	fmt.Println("✓ HookRun initialized successfully!")
	fmt.Println()
	fmt.Println("Created files:")
	fmt.Println("  - config.yaml")
	fmt.Printf("  - %s (template: %s)\n", hooksFile, template)
	fmt.Println()
	fmt.Println("Next steps:")
	fmt.Println("  1. Edit config.yaml to customize server settings")
	fmt.Printf("  2. Edit %s to define your webhook rules\n", hooksFile)
	fmt.Println("  3. Run 'hookrun validate' to check your configuration")
	fmt.Println("  4. Run 'hookrun start' to start the server")

	return nil
}

func writeFileWithNotice(path string, content string) error {
	if fileExists(path) {
		fmt.Printf("  Overwriting: %s\n", path)
	} else {
		fmt.Printf("  Creating:    %s\n", path)
	}
	return os.WriteFile(path, []byte(content), 0644)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
