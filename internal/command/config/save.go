package config

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"github.com/superfly/flyctl/client"
	"github.com/superfly/flyctl/internal/appconfig"
	"github.com/superfly/flyctl/internal/command"
	"github.com/superfly/flyctl/internal/command/apps"
	"github.com/superfly/flyctl/internal/flag"
	"github.com/superfly/flyctl/internal/prompt"
	"github.com/superfly/flyctl/internal/state"
)

func newSave() (cmd *cobra.Command) {
	const (
		short = "Save an app's config file"
		long  = `Save an application's configuration locally. The configuration data is
retrieved from the Fly service and saved in TOML format.`
	)
	cmd = command.New("save", short, long, runSave,
		command.RequireSession,
		command.RequireAppName,
	)
	cmd.Args = cobra.NoArgs
	flag.Add(cmd,
		flag.App(),
		flag.AppConfig(),
		flag.Yes(),
	)
	return
}

func runSave(ctx context.Context) error {
	var (
		err         error
		appName     = appconfig.NameFromContext(ctx)
		apiClient   = client.FromContext(ctx).API()
		autoConfirm = flag.GetBool(ctx, "yes")
	)
	appCompact, err := apiClient.GetAppCompact(ctx, appName)
	if err != nil {
		return fmt.Errorf("error getting app with name %s: %w", appName, err)
	}
	ctx, err = apps.BuildContext(ctx, appCompact)
	if err != nil {
		return err
	}
	cfg, err := appconfig.FromRemoteApp(ctx, appName)
	if err != nil {
		return err
	}

	path := state.WorkingDirectory(ctx)
	if flag.IsSpecified(ctx, "config") {
		path = flag.GetString(ctx, "config")
	}
	configfilename, err := appconfig.ResolveConfigFileFromPath(path)
	if err != nil {
		return err
	}

	if exists, _ := appconfig.ConfigFileExistsAtPath(configfilename); exists && !autoConfirm {
		confirmation, err := prompt.Confirmf(ctx,
			"An existing configuration file has been found\nOverwrite file '%s'", configfilename)
		if err != nil {
			return err
		}
		if !confirmation {
			return nil
		}
	}

	return cfg.WriteToDisk(ctx, configfilename)
}
