package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/ghodss/yaml"
	"github.com/spf13/cobra"

	"github.com/argoproj/argo-cd/errors"
	argocdclient "github.com/argoproj/argo-cd/pkg/apiclient"
	applicationpkg "github.com/argoproj/argo-cd/pkg/apiclient/application"
	argoappv1 "github.com/argoproj/argo-cd/pkg/apis/application/v1alpha1"
	"github.com/argoproj/argo-cd/util"
)

// NewApplicationResourceActionsCommand returns a new instance of an `argocd app actions` command
func NewApplicationResourceActionsCommand(clientOpts *argocdclient.ClientOptions) *cobra.Command {
	var command = &cobra.Command{
		Use:   "actions",
		Short: "Manage Resource actions",
		Run: func(c *cobra.Command, args []string) {
			c.HelpFunc()(c, args)
			os.Exit(1)
		},
	}
	command.AddCommand(NewApplicationResourceActionsListCommand(clientOpts))
	command.AddCommand(NewApplicationResourceActionsRunCommand(clientOpts))
	return command
}

// NewApplicationResourceActionsListCommand returns a new instance of an `argocd app actions list` command
func NewApplicationResourceActionsListCommand(clientOpts *argocdclient.ClientOptions) *cobra.Command {
	var namespace string
	var kind string
	var group string
	var resourceName string
	var output string
	var command = &cobra.Command{
		Use:   "list APPNAME",
		Short: "Lists available actions on a resource",
	}
	command.Run = func(c *cobra.Command, args []string) {
		if len(args) != 1 {
			c.HelpFunc()(c, args)
			os.Exit(1)
		}
		appName := args[0]
		conn, appIf := argocdclient.NewClientOrDie(clientOpts).NewApplicationClientOrDie()
		defer util.Close(conn)
		ctx := context.Background()
		resources, err := appIf.ManagedResources(ctx, &applicationpkg.ResourcesQuery{ApplicationName: &appName})
		errors.CheckError(err)
		filteredObjects := filterResources(command, resources.Items, group, kind, namespace, resourceName, true)
		availableActions := make(map[string][]argoappv1.ResourceAction)
		for i := range filteredObjects {
			obj := filteredObjects[i]
			gvk := obj.GroupVersionKind()
			availActionsForResource, err := appIf.ListResourceActions(ctx, &applicationpkg.ApplicationResourceRequest{
				Name:         &appName,
				Namespace:    obj.GetNamespace(),
				ResourceName: obj.GetName(),
				Group:        gvk.Group,
				Kind:         gvk.Kind,
			})
			errors.CheckError(err)
			availableActions[gvk.Group+"\t"+gvk.Kind+"\t"+obj.GetName()] = availActionsForResource.Actions
		}

		var keys []string
		for key := range availableActions {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		switch output {
		case "yaml":
			yamlBytes, err := yaml.Marshal(availableActions)
			errors.CheckError(err)
			fmt.Println(string(yamlBytes))
		case "json":
			jsonBytes, err := json.MarshalIndent(availableActions, "", "  ")
			errors.CheckError(err)
			fmt.Println(string(jsonBytes))
		case "":
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintf(w, "GROUP\tKIND\tNAME\tACTION\tAVAILABLE\n")
			fmt.Println()
			for key := range availableActions {
				for i := range availableActions[key] {
					action := availableActions[key][i]
					fmt.Fprintf(w, "%s\t%s\t%s\n", key, action.Name, strconv.FormatBool(action.Available))

				}
			}
			_ = w.Flush()
		}
	}
	command.Flags().StringVar(&resourceName, "resource-name", "", "Name of resource")
	command.Flags().StringVar(&kind, "kind", "", "Kind")
	command.Flags().StringVar(&group, "group", "", "Group")
	command.Flags().StringVar(&namespace, "namespace", "", "Namespace")
	command.Flags().StringVarP(&output, "out", "o", "", "Output format. One of: yaml, json")

	return command
}

// NewApplicationResourceActionsRunCommand returns a new instance of an `argocd app actions run` command
func NewApplicationResourceActionsRunCommand(clientOpts *argocdclient.ClientOptions) *cobra.Command {
	var namespace string
	var resourceName string
	var kindArg string
	var all bool
	var command = &cobra.Command{
		Use:   "run APPNAME ACTION",
		Short: "Runs an available action on resource(s)",
	}

	command.Flags().StringVar(&resourceName, "resource-name", "", "Name of resource")
	command.Flags().StringVar(&namespace, "namespace", "", "Namespace")
	command.Flags().StringVar(&kindArg, "kind", "", "Kind")
	command.Flags().BoolVar(&all, "all", false, "Indicates whether to run the action on multiple matching resources")

	command.Run = func(c *cobra.Command, args []string) {
		if len(args) != 2 {
			c.HelpFunc()(c, args)
			os.Exit(1)
		}
		appName := args[0]
		actionName := args[1]

		conn, appIf := argocdclient.NewClientOrDie(clientOpts).NewApplicationClientOrDie()
		defer util.Close(conn)
		ctx := context.Background()
		resources, err := appIf.ManagedResources(ctx, &applicationpkg.ResourcesQuery{ApplicationName: &appName})
		errors.CheckError(err)

		var group string
		var kind string
		var actionNameOnly string
		// Backwards comparability for running resume actions
		if actionName == "resume" && kindArg == "Rollout" {
			group = "argoproj.io"
			kind = "Rollout"
			actionNameOnly = "resume"
			commandTail := ""
			if resourceName != "" {
				commandTail += " --resource-name " + resourceName
			}
			if namespace != "" {
				commandTail += " --namespace " + namespace
			}
			if all {
				commandTail += " --all"
			}
			fmt.Printf("\nWarning: this syntax for running the \"resume\" action has been deprecated. Please run the action as\n\n\targocd app actions run %s argoproj.io/Rollout/resume%s\n\n", appName, commandTail)
		} else {
			group, kind, actionNameOnly = parseActionName(actionName)
		}

		filteredObjects := filterResources(command, resources.Items, group, kind, namespace, resourceName, all)
		for i := range filteredObjects {
			obj := filteredObjects[i]
			gvk := obj.GroupVersionKind()
			objResourceName := obj.GetName()
			_, err := appIf.RunResourceAction(context.Background(), &applicationpkg.ResourceActionRunRequest{
				Name:         &appName,
				Namespace:    obj.GetNamespace(),
				ResourceName: objResourceName,
				Group:        gvk.Group,
				Kind:         gvk.Kind,
				Action:       actionNameOnly,
			})
			errors.CheckError(err)
		}
	}
	return command
}

func parseActionName(action string) (string, string, string) {
	actionSplit := strings.Split(action, "/")
	if len(actionSplit) != 3 {
		log.Fatal("Action name is malformed")
	}
	return actionSplit[0], actionSplit[1], actionSplit[2]
}
