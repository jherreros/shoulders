package cmd

import (
	"context"
	"os/signal"
	"syscall"
	"time"

	"github.com/jherreros/shoulders/shoulders-cli/internal/kube"
	"github.com/spf13/cobra"
)

const (
	headlampNamespace = "headlamp"
	headlampService   = "headlamp"
)

var portalCmd = &cobra.Command{
	Use:   "portal",
	Short: "Open the Shoulders portal (Headlamp UI)",
	RunE: func(cmd *cobra.Command, args []string) error {
		const portalPath = "/c/main/shoulders"
		headlampOIDCURL := currentConfig.HeadlampURL()
		headlampHost := targetCluster.HeadlampHost()

		if isHostPortReachable(headlampHost, "80", 1500*time.Millisecond) {
			portalURL := headlampOIDCURL + portalPath
			cmd.Printf("Opening Shoulders portal at %s\n", portalURL)
			cmd.Printf("Sign in with Dex users (for example: admin@example.com / password).\n")
			if err := openBrowser(portalURL); err == nil {
				return nil
			}
		}

		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		cmd.Printf("Headlamp Gateway URL not reachable; falling back to local port-forward on http://localhost:4466\n")

		stopCh, _, err := kube.PortForwardService(ctx, kubeconfig, headlampNamespace, headlampService, 4466, 80)
		if err != nil {
			return err
		}
		defer close(stopCh)

		go func() {
			time.Sleep(2 * time.Second)
			_ = openBrowser("http://localhost:4466/c/main/shoulders")
		}()

		<-ctx.Done()
		return nil
	},
}
