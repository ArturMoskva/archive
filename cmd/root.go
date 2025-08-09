package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "archive",
	Short: "Простой архиватор",
}

func Execute(){

    if	err := rootCmd.Execute() ; err != nil {
		handleErr(err)
	}
}

func handleErr( err error){

	fmt.Fprintln(os.Stderr , err)
	os.Exit(1)
}