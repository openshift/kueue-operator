package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/openshift/kueue-operator/pkg/cmd/operator"
)

func main() {
	command := NewKueueOperatorCommand()
	if err := command.Execute(); err != nil {
		_, err := fmt.Fprintf(os.Stderr, "%v\n", err)
		if err != nil {
			fmt.Printf("Unable to print err to stderr: %v", err)
		}
		os.Exit(1)
	}
}

func NewKueueOperatorCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "kueue-operator",
		Short: "OpenShift cluster kueue operator",
		Run: func(cmd *cobra.Command, args []string) {
			err := cmd.Help()
			if err != nil {
				fmt.Printf("Unable to print help: %v", err)
			}
			os.Exit(1)
		},
	}

	cmd.AddCommand(operator.NewOperator())
	return cmd
}
