package cmd

import (
	"context"
	"os/signal"
	"syscall"
	"time"

	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	"github.com/spf13/cobra"
)

var dashboardCmd = &cobra.Command{
	Use:   "dashboard",
	Short: "Open Grafana dashboard",
	RunE: func(cmd *cobra.Command, args []string) error {
		grafanaOIDCURL := selectedGrafanaURL(targetCluster)
		grafanaHost := targetCluster.GrafanaHost()

		if isHostPortReachable(grafanaHost, "80", 1500*time.Millisecond) {
			cmd.Printf("Opening Grafana via Gateway OIDC at %s\n", grafanaOIDCURL)
			cmd.Printf("Sign in with Dex users (for example: admin@example.com / password).\n")
			if err := openBrowser(grafanaOIDCURL); err == nil {
				return nil
			}
		}

		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		cmd.Printf("Grafana Gateway URL not reachable; falling back to local port-forward on http://localhost:3000\n")

		stopCh, _, err := kube.PortForwardService(ctx, kubeconfig, "observability", "victoria-metrics-k8s-stack-grafana", 3000, 80)
		if err != nil {
			return err
		}
		defer close(stopCh)

		creds, err := kube.GetGrafanaCredentials(ctx, kubeconfig, "observability", "victoria-metrics-k8s-stack-grafana")
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
