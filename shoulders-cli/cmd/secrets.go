package cmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"github.com/spf13/cobra"

	"github.com/jherreros/shoulders/shoulders-cli/internal/bootstrap"
	"github.com/jherreros/shoulders/shoulders-cli/internal/config"
)

var secretsForce bool

var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage the age key that decrypts SOPS-encrypted manifests",
	Long: `Shoulders delivers secrets with SOPS and age.

Flux decrypts *.sops.yaml in the cluster; the CLI's job is to put the age
identity in the flux-system/sops-age Secret of every cluster. The identity is
kept outside the repository, in the sops CLI's own key file by default.`,
}

var secretsInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Generate the age key and write .sops.yaml",
	Long: `Generate an age identity for this machine, create the flux-system/sops-age
Secret on the next 'shoulders up', and write a .sops.yaml creation rule so
'sops -e' encrypts to the right recipient without extra flags.

The private key never enters git. The .sops.yaml file contains only the public
recipient, so it is safe to commit — commit it when the team shares one key,
and keep it out of git when every machine has its own.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, _, err := sopsKeyPath()
		if err != nil {
			return err
		}

		if existing, readErr := os.ReadFile(path); readErr == nil && !secretsForce {
			key, err := bootstrap.ParseSopsAgeIdentity(existing)
			if err != nil {
				return fmt.Errorf("%s exists but is not a usable age identity: %w (pass --force to replace it)", path, err)
			}
			identity, err := age.ParseX25519Identity(key)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			fmt.Printf("Age key already exists: %s\nRecipient: %s\n", path, identity.Recipient())
			return writeSopsConfig(identity.Recipient().String())
		} else if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
			return fmt.Errorf("read %s: %w", path, readErr)
		}

		identity, err := age.GenerateX25519Identity()
		if err != nil {
			return fmt.Errorf("generate an age identity: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
		}
		contents := fmt.Sprintf("# created by shoulders secrets init\n# public key: %s\n%s\n",
			identity.Recipient(), identity.String())
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		fmt.Printf("Wrote age identity: %s\nRecipient: %s\n", path, identity.Recipient())
		fmt.Println("\nNext: run 'shoulders up' to publish the key as flux-system/sops-age,")
		fmt.Printf("Key file: %s (the sops CLI reads it too)\n", path)
		fmt.Println("then encrypt a manifest with 'sops -e -i <file>.sops.yaml'.")
		return writeSopsConfig(identity.Recipient().String())
	},
}

// sopsKeyPath resolves the key path from the config, with the default applied.
func sopsKeyPath() (string, bool, error) {
	if currentConfig == nil {
		path, err := config.DefaultAgeKeyFile()
		return path, false, err
	}
	return currentConfig.Platform.Sops.KeyFile()
}

// attachSopsAgeKey adds the age identity to a Flux source when one exists.
//
// A missing default key means this topology does not use SOPS yet, so nothing
// changes. A missing *explicit* key is an error: the operator asked for
// decryption, and installing without it would fail later as a flux decryption
// error on a Kustomization that looks unrelated to the key file.
func attachSopsAgeKey(source bootstrap.FluxSource) (bootstrap.FluxSource, error) {
	path, explicit, err := sopsKeyPath()
	if err != nil {
		return source, err
	}
	identity, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if explicit {
			return source, fmt.Errorf("platform.sops.ageKeyFile %s does not exist; run 'shoulders secrets init'", path)
		}
		return source, nil
	}
	if err != nil {
		return source, fmt.Errorf("read the age key %s: %w", path, err)
	}
	key, err := bootstrap.ParseSopsAgeIdentity(identity)
	if err != nil {
		return source, fmt.Errorf("%s: %w", path, err)
	}
	if _, err := age.ParseX25519Identity(key); err != nil {
		return source, fmt.Errorf("%s: %w", path, err)
	}
	source.SopsAgeKey = identity
	return source, nil
}

// writeSopsConfig writes .sops.yaml in the working directory.
//
// It only rewrites an existing file when the recipient it lists is not this
// one: two machines sharing a checkout should not silently overwrite each
// other's creation rule.
func writeSopsConfig(recipient string) error {
	path := ".sops.yaml"
	current, err := os.ReadFile(path)
	if err == nil {
		if strings.Contains(string(current), recipient) {
			return nil
		}
		fmt.Printf("\n%s already lists a different recipient; leaving it alone.\n"+
			"\n'sops -e' will encrypt to that key. Delete the file if that key is not yours.\n", path)
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	body := fmt.Sprintf(`# Written by 'shoulders secrets init'. The recipient is a public key and is
# meant to be committed; the private key stays in ~/.shoulders/age.key.
creation_rules:
  - path_regex: \.sops\.yaml$
    age: %s
`, recipient)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	fmt.Printf("Wrote %s for recipient %s\n", path, recipient)
	return nil
}

func init() {
	secretsCmd.AddCommand(secretsInitCmd)
	secretsInitCmd.Flags().BoolVar(&secretsForce, "force", false, "Replace an existing identity")
}
