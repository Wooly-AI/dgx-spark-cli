package playbook

import (
	"fmt"
	"strings"

	"github.com/weatherman/dgx-manager/internal/ssh"
)

// runDMR handles Docker Model Runner helper commands
func (m *Manager) runDMR(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("dmr command required. Usage: dgx run dmr <setup|install|update|status|logs|list|pull|run|uninstall>")
	}

	command := args[0]
	rest := args[1:]

	switch command {
	case "setup":
		return m.dmrSetup()
	case "install":
		return m.dmrInstallRunner()
	case "update":
		return m.dmrUpdateRunner()
	case "status":
		return m.dmrStatus()
	case "logs":
		return m.dmrLogs(rest)
	case "list":
		return m.dmrList(rest)
	case "pull":
		if len(rest) == 0 {
			return fmt.Errorf("model reference required. Usage: dgx run dmr pull <model> [flags]")
		}
		return m.dmrPull(rest[0], rest[1:])
	case "run":
		if len(rest) == 0 {
			return fmt.Errorf("model reference required. Usage: dgx run dmr run <model> \"prompt\"")
		}
		model := rest[0]
		prompt := ""
		if len(rest) > 1 {
			prompt = strings.Join(rest[1:], " ")
		}
		return m.dmrRun(model, prompt)
	case "uninstall":
		return m.dmrUninstall()
	default:
		return fmt.Errorf("unknown dmr command: %s", command)
	}
}

func (m *Manager) dmrSetup() error {
	fmt.Println("Installing Docker Model Runner prerequisites (Docker Engine, plugin, GPU runtime)...")
	fmt.Println("Warning: This may download and run scripts from https://get.docker.com with sudo.")
	fmt.Print("Continue? [Y/n]: ")
	var confirm string
	fmt.Scanln(&confirm)
	if confirm != "" && strings.ToLower(confirm) != "y" {
		fmt.Println("Setup cancelled.")
		return nil
	}

	script := `set -euo pipefail
if ! command -v docker >/dev/null 2>&1; then
  curl -fsSL https://get.docker.com | sudo sh
fi
if command -v apt-get >/dev/null 2>&1; then
  sudo apt-get update
  if ! dpkg -s docker-model-plugin >/dev/null 2>&1; then
    sudo apt-get install -y docker-model-plugin
  fi
elif command -v dnf >/dev/null 2>&1; then
  sudo dnf install -y docker-model-plugin
fi
if command -v apt-get >/dev/null 2>&1; then
  if ! dpkg -s nvidia-container-toolkit >/dev/null 2>&1; then
    sudo apt-get install -y nvidia-container-toolkit
  fi
elif command -v dnf >/dev/null 2>&1; then
  sudo dnf install -y nvidia-container-toolkit
fi
if command -v nvidia-ctk >/dev/null 2>&1; then
  sudo nvidia-ctk runtime configure --runtime=docker >/dev/null 2>&1 || true
  sudo systemctl restart docker >/dev/null 2>&1 || true
fi
sudo usermod -aG docker $(whoami) >/dev/null 2>&1 || true
`

	output, err := m.sshClient.Execute(script)
	if err != nil {
		return fmt.Errorf("failed to set up Docker Model Runner prerequisites: %w", err)
	}

	if strings.TrimSpace(output) != "" {
		fmt.Println(output)
	}
	fmt.Println("Prerequisites installed. Log out/in to apply docker group membership if prompted.")
	return nil
}

func (m *Manager) dmrInstallRunner() error {
	fmt.Println("Installing Docker Model Runner controller container...")
	output, err := m.sshClient.Execute("docker model install-runner --gpu auto")
	if err != nil {
		return fmt.Errorf("failed to install Docker Model Runner: %w", err)
	}
	fmt.Println(output)
	fmt.Println("Docker Model Runner installed. Use 'dgx run dmr status' to verify.")
	return nil
}

func (m *Manager) dmrUpdateRunner() error {
	fmt.Println("Updating Docker Model Runner...")
	cmd := "docker model uninstall-runner --images && docker model install-runner --gpu auto"
	output, err := m.sshClient.Execute(cmd)
	if err != nil {
		return fmt.Errorf("failed to update Docker Model Runner: %w", err)
	}
	fmt.Println(output)
	return nil
}

func (m *Manager) dmrStatus() error {
	fmt.Println("Checking Docker Model Runner status...")
	output, err := m.sshClient.Execute("docker model status --json || docker model status || true")
	if err != nil {
		return fmt.Errorf("failed to get Docker Model Runner status: %w", err)
	}
	fmt.Println(output)
	return nil
}

func (m *Manager) dmrLogs(args []string) error {
	cmd := "docker model logs"
	if len(args) == 0 {
		cmd += " --tail 200"
	} else {
		cmd += " " + strings.Join(args, " ")
	}
	output, err := m.sshClient.Execute(cmd)
	if err != nil {
		return fmt.Errorf("failed to retrieve Docker Model Runner logs: %w", err)
	}
	fmt.Println(output)
	return nil
}

func (m *Manager) dmrList(args []string) error {
	cmd := "docker model list"
	if len(args) > 0 {
		cmd += " " + strings.Join(args, " ")
	}
	output, err := m.sshClient.Execute(cmd)
	if err != nil {
		return fmt.Errorf("failed to list models: %w", err)
	}
	fmt.Println(output)
	return nil
}

func (m *Manager) dmrPull(model string, extra []string) error {
	if model == "" {
		return fmt.Errorf("model reference required")
	}
	cmd := fmt.Sprintf("docker model pull %s", ssh.ShellQuote(model))
	if len(extra) > 0 {
		cmd += " " + strings.Join(extra, " ")
	}
	output, err := m.sshClient.Execute(cmd)
	if err != nil {
		return fmt.Errorf("failed to pull model: %w", err)
	}
	fmt.Println(output)
	return nil
}

func (m *Manager) dmrRun(model string, prompt string) error {
	if prompt == "" {
		fmt.Println("Interactive chat requires a TTY. Run 'dgx connect' and use 'docker model run' directly for interactive sessions, or supply a prompt: dgx run dmr run <model> \"prompt\".")
		return nil
	}
	fmt.Printf("Running %s via Docker Model Runner...\n", model)
	cmd := fmt.Sprintf("docker model run %s %s", ssh.ShellQuote(model), ssh.ShellQuote(prompt))
	output, err := m.sshClient.Execute(cmd)
	if err != nil {
		return fmt.Errorf("failed to run model: %w", err)
	}
	fmt.Println(output)
	return nil
}

func (m *Manager) dmrUninstall() error {
	fmt.Println("Removing Docker Model Runner and cached images...")
	output, err := m.sshClient.Execute("docker model uninstall-runner --images")
	if err != nil {
		return fmt.Errorf("failed to uninstall Docker Model Runner: %w", err)
	}
	fmt.Println(output)
	return nil
}
