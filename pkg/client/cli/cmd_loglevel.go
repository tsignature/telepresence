package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/telepresenceio/telepresence/rpc/v2/connector"
	"github.com/telepresenceio/telepresence/rpc/v2/daemon"
	"github.com/telepresenceio/telepresence/rpc/v2/manager"
	"github.com/telepresenceio/telepresence/v2/pkg/client/cli/cliutil"
	"github.com/telepresenceio/telepresence/v2/pkg/client/errcat"
)

const defaultDuration = 30 * time.Minute

type logLevelSetter struct {
	duration   time.Duration
	localOnly  bool
	remoteOnly bool
}

func logLevelArg(cmd *cobra.Command, args []string) error {
	if len(args) != 1 {
		return errcat.User.New("accepts exactly one argument (the log level)")
	}
	lvl, err := logrus.ParseLevel(args[0])
	if err != nil {
		return errcat.User.New(err)
	}
	switch lvl {
	case logrus.PanicLevel, logrus.FatalLevel:
		return errcat.User.Newf("unsupported log level: %s", lvl)
	}
	return nil
}

func loglevelCommand() *cobra.Command {
	lvs := logrus.AllLevels[2:] // Don't include `panic` and `fatal`
	lvStrs := make([]string, len(lvs))
	for i, lv := range lvs {
		lvStrs[i] = lv.String()
	}
	lls := logLevelSetter{}
	cmd := &cobra.Command{
		Use:       fmt.Sprintf("loglevel <%s>", strings.Join(lvStrs, ",")),
		Args:      logLevelArg,
		Short:     "Temporarily change the log-level of the traffic-manager, traffic-agent, and user and root daemons",
		RunE:      lls.setTempLogLevel,
		ValidArgs: lvStrs,
	}
	flags := cmd.Flags()
	flags.DurationVarP(&lls.duration, "duration", "d", defaultDuration, "The time that the log-level will be in effect (0s means indefinitely)")
	flags.BoolVarP(&lls.localOnly, "local-only", "l", false, "Only affect the user and root daemons")
	flags.BoolVarP(&lls.remoteOnly, "remote-only", "r", false, "Only affect the traffic-manager and traffic-agents")
	return cmd
}

func (lls *logLevelSetter) setTempLogLevel(cmd *cobra.Command, args []string) error {
	if lls.localOnly && lls.remoteOnly {
		return errcat.User.New("the local-only and remote-only options are mutually exclusive")
	}

	return withConnector(cmd, true, func(ctx context.Context, connectorClient connector.ConnectorClient, connInfo *connector.ConnectInfo) error {
		rq := &manager.LogLevelRequest{LogLevel: args[0], Duration: durationpb.New(lls.duration)}
		if !lls.remoteOnly {
			_, err := connectorClient.SetLogLevel(ctx, rq)
			if err != nil {
				return err
			}

			err = cliutil.WithStartedDaemon(ctx, func(ctx context.Context, daemonClient daemon.DaemonClient) error {
				_, err := daemonClient.SetLogLevel(ctx, rq)
				return err
			})
			if err != nil {
				return err
			}
		}

		if !lls.localOnly {
			err := cliutil.WithManager(ctx, func(ctx context.Context, managerClient manager.ManagerClient) error {
				_, err := managerClient.SetLogLevel(ctx, rq)
				return err
			})
			return err
		}
		return nil
	})
}
