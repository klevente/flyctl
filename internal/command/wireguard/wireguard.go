package wireguard

import (
	"context"
	"fmt"
	"os"

	"github.com/olekukonko/tablewriter"
	"github.com/pkg/errors"
	"github.com/spf13/viper"
	"github.com/superfly/flyctl/agent"
	"github.com/superfly/flyctl/client"
	"github.com/superfly/flyctl/flyctl"
	"github.com/superfly/flyctl/internal/config"
	"github.com/superfly/flyctl/internal/flag"
	"github.com/superfly/flyctl/internal/render"
	"github.com/superfly/flyctl/internal/wireguard"
	"github.com/superfly/flyctl/iostreams"
	"github.com/superfly/flyctl/terminal"
)

func runWireguardList(ctx context.Context) error {
	io := iostreams.FromContext(ctx)
	apiClient := client.FromContext(ctx).API()

	org, err := orgByArg(ctx)
	if err != nil {
		return err
	}

	peers, err := apiClient.GetWireGuardPeers(ctx, org.Slug)
	if err != nil {
		return err
	}

	if config.FromContext(ctx).JSONOutput {
		render.JSON(io.Out, peers)
		return nil
	}

	table := tablewriter.NewWriter(io.Out)

	table.SetHeader([]string{
		"Name",
		"Region",
		"Peer IP",
	})

	for _, peer := range peers {
		table.Append([]string{peer.Name, peer.Region, peer.Peerip})
	}

	table.Render()

	return nil
}

func runWireguardWebsockets(ctx context.Context) error {
	io := iostreams.FromContext(ctx)

	switch flag.FirstArg(ctx) {
	case "enable":
		viper.Set(flyctl.ConfigWireGuardWebsockets, true)

	case "disable":
		viper.Set(flyctl.ConfigWireGuardWebsockets, false)

	default:
		fmt.Fprintf(io.Out, "bad arg: flyctl wireguard websockets (enable|disable)\n")
	}

	if err := flyctl.SaveConfig(); err != nil {
		return errors.Wrap(err, "error saving config file")
	}

	tryKillingAgent := func() error {
		client, err := agent.DefaultClient(ctx)
		if err == agent.ErrAgentNotRunning {
			return nil
		} else if err != nil {
			return err
		}

		return client.Kill(ctx)
	}

	// kill the agent if necessary, if that fails print manual instructions
	if err := tryKillingAgent(); err != nil {
		terminal.Debugf("error stopping the agent: %s", err)
		fmt.Fprintf(io.Out, "Run `flyctl agent restart` to make changes take effect.\n")
	}

	return nil
}

func runWireguardReset(ctx context.Context) error {
	io := iostreams.FromContext(ctx)

	org, err := orgByArg(ctx)
	if err != nil {
		return err
	}

	apiClient := client.FromContext(ctx).API()
	agentclient, err := agent.Establish(config.NewContext(context.Background(), config.FromContext(ctx)), apiClient)
	if err != nil {
		return err
	}

	conf, err := agentclient.Reestablish(context.Background(), org.Slug)
	if err != nil {
		return err
	}

	fmt.Fprintf(io.Out, "New WireGuard peer for organization '%s': '%s'\n", org.Slug, conf.WireGuardState.Name)
	return nil
}

func runWireguardCreate(ctx context.Context) error {
	io := iostreams.FromContext(ctx)
	apiClient := client.FromContext(ctx).API()

	org, err := orgByArg(ctx)
	if err != nil {
		return err
	}

	args := flag.Args(ctx)
	var region string
	var name string

	if len(args) > 1 && args[1] != "" {
		region = args[1]
	}

	if len(args) > 2 && args[2] != "" {
		name = args[2]
	}

	state, err := wireguard.Create(apiClient, org, region, name)
	if err != nil {
		return err
	}

	data := &state.Peer

	fmt.Fprintf(io.Out, `
!!!! WARNING: Output includes private key. Private keys cannot be recovered !!!!
!!!! after creating the peer; if you lose the key, you'll need to remove    !!!!
!!!! and re-add the peering connection.                                     !!!!
`)

	w, shouldClose, err := resolveOutputWriter(ctx, 3, "Filename to store WireGuard configuration in, or 'stdout': ")
	if err != nil {
		return err
	}
	if shouldClose {
		defer w.Close() // skipcq: GO-S2307
	}

	generateWgConf(data, state.LocalPrivate, w)

	if shouldClose {
		filename := w.(*os.File).Name()
		fmt.Fprintf(io.Out, "Wrote WireGuard configuration to %s; load in your WireGuard client\n", filename)
	}

	return nil
}

func runWireguardRemove(ctx context.Context) error {
	io := iostreams.FromContext(ctx)
	apiClient := client.FromContext(ctx).API()

	org, err := orgByArg(ctx)
	if err != nil {
		return err
	}

	args := flag.Args(ctx)
	var name string
	if len(args) >= 2 {
		name = args[1]
	} else {
		name, err = selectWireGuardPeer(ctx, apiClient, org.Slug)
		if err != nil {
			return err
		}
	}

	fmt.Fprintf(io.Out, "Removing WireGuard peer \"%s\" for organization %s\n", name, org.Slug)

	err = apiClient.RemoveWireGuardPeer(ctx, org, name)
	if err != nil {
		return err
	}

	fmt.Fprintln(io.Out, "Removed peer.")

	return wireguard.PruneInvalidPeers(ctx, apiClient)
}

func runWireguardStatus(ctx context.Context) error {
	io := iostreams.FromContext(ctx)
	apiClient := client.FromContext(ctx).API()

	org, err := orgByArg(ctx)
	if err != nil {
		return err
	}

	args := flag.Args(ctx)
	var name string
	if len(args) >= 2 {
		name = args[1]
	} else {
		name, err = selectWireGuardPeer(ctx, apiClient, org.Slug)
		if err != nil {
			return err
		}
	}

	status, err := apiClient.GetWireGuardPeerStatus(ctx, org.Slug, name)
	if err != nil {
		return err
	}

	fmt.Fprintf(io.Out, "Alive: %+v\n", status.Live)

	if status.WgError != "" {
		fmt.Fprintf(io.Out, "Gateway error: %s\n", status.WgError)
	}

	if !status.Live {
		return nil
	}

	if status.Endpoint != "" {
		fmt.Fprintf(io.Out, "Last Source Address: %s\n", status.Endpoint)
	}

	ago := ""
	if status.SinceAdded != "" {
		ago = " (" + status.SinceAdded + " ago)"
	}

	if status.LastHandshake != "" {
		fmt.Fprintf(io.Out, "Last Handshake At: %s%s\n", status.LastHandshake, ago)
	}

	ago = ""
	if status.SinceHandshake != "" {
		ago = " (" + status.SinceHandshake + " ago)"
	}

	fmt.Fprintf(io.Out, "Installed On Gateway At: %s%s\n", status.Added, ago)
	fmt.Fprintf(io.Out, "Traffic: rx:%d tx:%d\n", status.Rx, status.Tx)

	return nil
}
