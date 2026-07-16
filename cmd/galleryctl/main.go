package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	api "github.com/RecRivenVI/gallery/pkg/galleryapi"
	version "github.com/RecRivenVI/gallery/pkg/galleryversion"
)

func main() { os.Exit(run()) }

func run() int {
	flags := flag.NewFlagSet("galleryctl", flag.ContinueOnError)
	baseURL := flags.String("base-url", "http://127.0.0.1:8080", "galleryd API base URL")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return 2
	}
	command := "version"
	if flags.NArg() > 0 {
		command = flags.Arg(0)
	}
	switch command {
	case "version":
		fmt.Printf("galleryctl %s\n", version.Version)
		return 0
	case "health":
		client, err := api.NewClientWithResponses(*baseURL)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		response, err := client.GetHealthWithResponse(ctx)
		if err != nil || response.JSON200 == nil {
			fmt.Fprintln(os.Stderr, "galleryd health 请求失败")
			return 1
		}
		_ = json.NewEncoder(os.Stdout).Encode(response.JSON200)
		return 0
	default:
		fmt.Fprintln(os.Stderr, "未知命令；可用：version、health")
		return 2
	}
}
