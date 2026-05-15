package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"

	"github.com/fujiwara/terraform-provider-ecspresso/internal/provider"
)

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	flag.Parse()

	opts := providerserver.ServeOpts{
		Address: "registry.terraform.io/fujiwara/ecspresso",
		Debug:   debug,
	}

	if err := providerserver.Serve(context.Background(), provider.New(Version), opts); err != nil {
		log.Fatal(err.Error())
	}
}
