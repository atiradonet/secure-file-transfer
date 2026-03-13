package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

func newListCmd(sc StorageClient) *cobra.Command {
	var workspace, prefix string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List objects in the bucket",
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := getProject(cmd.Context())
			if err != nil {
				return err
			}
			return runList(cmd.Context(), sc, project, workspace, prefix)
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "", "Workspace name (required)")
	cmd.Flags().StringVar(&prefix, "prefix", "", "Filter by prefix")
	_ = cmd.MarkFlagRequired("workspace")
	return cmd
}

func runList(ctx context.Context, sc StorageClient, project, workspace, prefix string) error {
	if err := validateWorkspace(workspace); err != nil {
		return err
	}
	bucket, _ := resolve(workspace, project)

	objects, err := sc.ListObjects(ctx, bucket, prefix)
	if err != nil {
		return fmt.Errorf("listing objects: %w", err)
	}

	if len(objects) == 0 {
		fmt.Println("Bucket is empty.")
		return nil
	}

	fmt.Printf("%-60s  %12s  %s\n", "Object", "Size", "Updated")
	fmt.Println("-------------------------------------------------------------------------------------------")
	for _, obj := range objects {
		size := fmt.Sprintf("%d", obj.Size)
		updated := obj.Updated.UTC().Format("2006-01-02 15:04 UTC")
		fmt.Printf("%-60s  %12s  %s\n", obj.Name, size, updated)
	}
	return nil
}
