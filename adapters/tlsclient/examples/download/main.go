package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/Albert-Zhan/httpc/adapters/tlsclient/v2"
	"github.com/Albert-Zhan/httpc/v2/downloader"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

func main() {
	url := flag.String("url", "", "file URL")
	dst := flag.String("out", "download.bin", "new destination file")
	workers := flag.Int("workers", 4, "parallel workers (1 forces single stream)")
	flag.Parse()
	if *url == "" {
		log.Fatal("-url is required")
	}
	raw, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(),
		tls_client.WithClientProfile(profiles.DefaultClientProfile),
		tls_client.WithDisableHttp3(), tls_client.WithTimeoutSeconds(0),
	)
	if err != nil {
		log.Fatal(err)
	}
	defer raw.Close()
	client, err := tlsclient.New(raw)
	if err != nil {
		log.Fatal(err)
	}
	d, err := downloader.New(client, downloader.Options{Workers: *workers, ChunkSize: 8 << 20})
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	result, err := d.Download(ctx, *url, *dst)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%s: %d bytes (%s, %d workers)\n", result.Path, result.Size, result.Mode, result.WorkersUsed)
}
