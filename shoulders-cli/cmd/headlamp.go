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

var headlampCmd = &cobra.Command{
	Use:   "headlamp",
	Short: "Open Headlamp UI",
	RunE: func(cmd *cobra.Command, args []string) error {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()

		if token, err := kube.CreateServiceAccountToken(ctx, kubeconfig, headlampNamespace, headlampService); err == nil {
			cmd.Printf("Headlamp token (service account %s/%s):\n%s\n", headlampNamespace, headlampService, token)
		} else if token, err := kube.CreateServiceAccountToken(ctx, kubeconfig, headlampNamespace, "default"); err == nil {
			cmd.Printf("Headlamp token (service account %s/%s):\n%s\n", headlampNamespace, "default", token)
		} else {
			cmd.Printf("Unable to fetch a Headlamp token automatically.\n")
		}

		stopCh, _, err := kube.PortForwardService(ctx, kubeconfig, headlampNamespace, headlampService, 4466, 80)
		if err != nil {
			return err
		}
		defer close(stopCh)

		go func() {
			time.Sleep(2 * time.Second)
			_ = openBrowser("http://localhost:4466/shoulders")
		}()

		<-ctx.Done()
		return nil
	},
}
