package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"google.golang.org/api/googleapi"
)

func main() {
	ctx := context.Background()

	sc, err := newStorageClient(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to create storage client: %v\n", err)
		os.Exit(1)
	}

	root := &cobra.Command{
		Use:          "transfer",
		Short:        "Secure File Transfer — encrypt, upload, and generate signed download URLs",
		SilenceUsage: true,
	}
	root.SilenceErrors = true

	root.AddCommand(newUploadCmd(sc))
	root.AddCommand(newPackCmd(sc))
	root.AddCommand(newListCmd(sc))
	root.AddCommand(newDeleteCmd(sc))

	if err := root.ExecuteContext(ctx); err != nil {
		var gErr *googleapi.Error
		if errors.As(err, &gErr) {
			switch gErr.Code {
			case 403:
				fmt.Fprintf(os.Stderr, "Error: permission denied. Verify your account is listed in GCP_SIGNING_MEMBERS.\n"+
					"If you just provisioned the workspace, wait 90 s for IAM to propagate and retry.\n")
			case 404:
				fmt.Fprintf(os.Stderr, "Error: resource not found. Check the workspace name and that infrastructure has been provisioned.\n")
			default:
				fmt.Fprintf(os.Stderr, "Error: GCP API call failed (HTTP %d): %s\n", gErr.Code, gErr.Message)
			}
		} else {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}
		os.Exit(1)
	}
}
