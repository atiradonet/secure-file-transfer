package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	ctx := context.Background()

	sc, err := newStorageClient(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to create storage client: %v\n", err)
		os.Exit(1)
	}

	root := &cobra.Command{
		Use:   "transfer",
		Short: "Secure File Transfer — encrypt, upload, and generate signed download URLs",
	}

	root.AddCommand(newUploadCmd(sc))
	root.AddCommand(newPackCmd(sc))
	root.AddCommand(newListCmd(sc))
	root.AddCommand(newDeleteCmd(sc))

	if err := root.ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}
