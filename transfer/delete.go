package main

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
)

func newDeleteCmd(sc StorageClient) *cobra.Command {
	var workspace, object, confirm string
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Delete an object from the bucket",
		RunE: func(cmd *cobra.Command, args []string) error {
			project, err := getProject(cmd.Context())
			if err != nil {
				return err
			}
			return runDelete(cmd.Context(), sc, project, workspace, object, confirm)
		},
	}
	cmd.Flags().StringVar(&workspace, "workspace", "", "Workspace name (required)")
	cmd.Flags().StringVar(&object, "object", "", "Object name to delete (required)")
	cmd.Flags().StringVar(&confirm, "confirm", "", "Repeat the object name to confirm deletion (required)")
	_ = cmd.MarkFlagRequired("workspace")
	_ = cmd.MarkFlagRequired("object")
	_ = cmd.MarkFlagRequired("confirm")
	return cmd
}

func runDelete(ctx context.Context, sc StorageClient, project, workspace, object, confirm string) error {
	if err := validateWorkspace(workspace); err != nil {
		return err
	}
	if confirm != object {
		return fmt.Errorf("confirmation does not match object name.\nTo delete %q, pass --confirm %q", object, object)
	}
	bucket, _ := resolve(workspace, project)

	if err := sc.DeleteObject(ctx, bucket, object); err != nil {
		return fmt.Errorf("deleting object: %w", err)
	}
	fmt.Printf("Deleted gs://%s/%s\n", bucket, object)
	return nil
}
