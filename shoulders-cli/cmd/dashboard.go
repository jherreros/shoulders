package cmd

import (
	"context"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	"github.com/spf13/cobra"
)

var dashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Open Grafana dashboard",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		stopCh, _, err := kube.PortForwardService(ctx, kubeconfig, "observability", "kube-prometheus-stack-grafana", 3000, 80)
		if err != nil {
			return err
		}
		defer close(stopCh)

		creds, err := kube.GetGrafanaCredentials(ctx, kubeconfig, "observability", "kube-prometheus-stack-grafana")
		if err != nil {
			return err
		}
		cmd.Printf("Grafana credentials:\n  user: %s\n  password: %s\n", creds.Username, creds.Password)

		go func() {
			time.Sleep(2 * time.Second)
			_ = openBrowser("http://localhost:3000")
		}()

		<-ctx.Done()
		return nil
	},
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
