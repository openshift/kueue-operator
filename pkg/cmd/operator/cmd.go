package operator

import (
	"github.com/spf13/cobra"
	"k8s.io/utils/clock"

	"github.com/openshift/kueue-operator/pkg/operator"
	"github.com/openshift/kueue-operator/pkg/version"
	"github.com/openshift/library-go/pkg/controller/controllercmd"
)

func NewOperator() *cobra.Command {
	cmd := controllercmd.
		NewControllerCommandConfig("openshift-kueue-operator", version.Get(), operator.RunOperator, clock.RealClock{}).
		NewCommand()
	cmd.Use = "operator"
	cmd.Short = "Start the Cluster Kueue Operator"

	return cmd
}
