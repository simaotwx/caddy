// Copyright 2015-2021 Matthew Holt and The Caddy Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package fastcgi

import (
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"strconv"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	caddycmd "github.com/caddyserver/caddy/v2/cmd"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/fileserver"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/reverseproxy"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp/rewrite"
)

func init() {
	caddycmd.RegisterCommand(caddycmd.Command{
		Name:  "php-fastcgi",
		Func:  cmdPHPFastCGI,
		Usage: "[--from <addr>] [--to <addr>] --root <dir>",
		Short: "A quick and production-ready PHP FastCGI server",
		Long: `
A simple but production-ready PHP FastCGI server. Useful for quick deployments,
demos, and development.

Unless otherwise specified in the addresses, the --from address will be
assumed to be HTTPS if a hostname is given.

If the --from address has a host or IP, Caddy will attempt to serve the
proxy over HTTPS with a certificate (unless overridden by the HTTP scheme
or port).

The --root parameter needs to specified as a directory, equivalent to the
"root" subdirective of php_fastcgi.
`,
		Flags: func() *flag.FlagSet {
			fs := flag.NewFlagSet("php-fastcgi", flag.ExitOnError)
			fs.String("from", "localhost", "Address on which to receive traffic")
			fs.String("to", "", "Upstream address to which to to proxy traffic")
			fs.String("root", "", "Directory to process PHP files from")
			return fs
		}(),
	})
}

func cmdPHPFastCGI(fs caddycmd.Flags) (int, error) {
	caddy.TrapSignals()

	from := fs.String("from")
	to := fs.String("to")
	root := fs.String("root")

	fromAddr, toAddr, cfg, err := processPHPFastCGI(from, to, root)
	if err != nil {
		return caddy.ExitCodeFailedStartup, err
	}

	err = caddy.Run(cfg)
	if err != nil {
		return caddy.ExitCodeFailedStartup, err
	}

	fmt.Printf("Caddy proxying PHP FastCGI %s -> %s\n", fromAddr.String(), toAddr.String())

	select {}
}

func processPHPFastCGI(from string, to string, root string) (httpcaddyfile.Address, httpcaddyfile.Address, *caddy.Config, error, ) {
	var err error

	httpPort := strconv.Itoa(caddyhttp.DefaultHTTPPort)
	httpsPort := strconv.Itoa(caddyhttp.DefaultHTTPSPort)
	fastCGIPort := strconv.Itoa(DefaultFastCGIPort)

	fromAddr := httpcaddyfile.Address{}
	toAddr := httpcaddyfile.Address{}

	if to == "" {
		return fromAddr, toAddr, nil, fmt.Errorf("--to is required")
	}
	if root == "" {
		return fromAddr, toAddr, nil, fmt.Errorf("--to is required")
	}

	// set up the downstream address; assume missing information from given parts
	fromAddr, err = httpcaddyfile.ParseAddress(from)
	if err != nil {
		return fromAddr, toAddr, nil, fmt.Errorf("invalid downstream address %s: %v", from, err)
	}
	if fromAddr.Path != "" {
		return fromAddr, toAddr, nil, fmt.Errorf("paths are not allowed: %s", from)
	}
	if fromAddr.Port == "" {
		if fromAddr.Scheme == "http" {
			fromAddr.Port = httpPort
		} else if fromAddr.Scheme == "https" {
			fromAddr.Port = httpsPort
		}
	}
	if fromAddr.Scheme == "" {
		if fromAddr.Port == httpPort || fromAddr.Host == "" {
			fromAddr.Scheme = "http"
		} else {
			fromAddr.Scheme = "https"
		}
	}

	// set up the upstream address; assume missing information from given parts
	toAddr, err = httpcaddyfile.ParseAddress(to)
	if err != nil {
		return fromAddr, toAddr, nil, fmt.Errorf("invalid upstream address %s: %v", to, err)
	}
	if toAddr.Path != "" {
		return fromAddr, toAddr, nil, fmt.Errorf("paths are not allowed: %s", to)
	}
	if toAddr.Port == "" {
		toAddr.Port = fastCGIPort
	}
	switch toAddr.Scheme {
	case "unix":
	case "":
		toAddr.Scheme = "fastcgi"
	default:
		return fromAddr, toAddr, nil, fmt.Errorf(
			"invalid upstream scheme %s: should be omitted, 'fastcgi' or 'unix'", toAddr.Scheme)
	}

	// set up the transport for FastCGI, and specifically PHP
	fcgiTransport := Transport{
		Root: root,
	}

	// set up the set of file extensions allowed to execute PHP code
	extensions := []string{".php"}

	// set the default index file for the try_files rewrites
	indexFile := "index.php"

	// set up a route list that we'll append to
	routes := caddyhttp.RouteList{}

	// set the list of allowed path segments on which to split
	fcgiTransport.SplitPath = extensions

	// route to redirect to canonical path if index PHP file
	redirMatcherSet := caddy.ModuleMap{
		"file": caddyconfig.JSON(fileserver.MatchFile{
			TryFiles: []string{"{http.request.uri.path}/" + indexFile},
		}, nil),
		"not": caddyconfig.JSON(caddyhttp.MatchNot{
			MatcherSetsRaw: []caddy.ModuleMap{
				{
					"path": caddyconfig.JSON(caddyhttp.MatchPath{"*/"}, nil),
				},
			},
		}, nil),
	}
	redirHandler := caddyhttp.StaticResponse{
		StatusCode: caddyhttp.WeakString(strconv.Itoa(http.StatusPermanentRedirect)),
		Headers:    http.Header{"Location": []string{"{http.request.uri.path}/"}},
	}
	redirRoute := caddyhttp.Route{
		MatcherSetsRaw: []caddy.ModuleMap{redirMatcherSet},
		HandlersRaw:    []json.RawMessage{caddyconfig.JSONModuleObject(redirHandler, "handler", "static_response", nil)},
	}

	// Use a reasonable default
	tryFiles := []string{"{http.request.uri.path}", "{http.request.uri.path}/" + indexFile, indexFile}

	// route to rewrite to PHP index file
	rewriteMatcherSet := caddy.ModuleMap{
		"file": caddyconfig.JSON(fileserver.MatchFile{
			TryFiles:  tryFiles,
			SplitPath: extensions,
		}, nil),
	}
	rewriteHandler := rewrite.Rewrite{
		URI: "{http.matchers.file.relative}",
	}
	rewriteRoute := caddyhttp.Route{
		MatcherSetsRaw: []caddy.ModuleMap{rewriteMatcherSet},
		HandlersRaw:    []json.RawMessage{caddyconfig.JSONModuleObject(rewriteHandler, "handler", "rewrite", nil)},
	}

	routes = append(routes, redirRoute, rewriteRoute)

	// create the reverse proxy handler which uses our FastCGI transport
	rpHandler := &reverseproxy.Handler{
		TransportRaw: caddyconfig.JSONModuleObject(fcgiTransport, "protocol", "fastcgi", nil),
		Upstreams:    reverseproxy.UpstreamPool{{Dial: net.JoinHostPort(toAddr.Host, toAddr.Port)}},
	}

	// route to actually reverse proxy requests to PHP files;
	// match only requests that are for PHP files
	var pathList []string
	for _, ext := range extensions {
		pathList = append(pathList, "*"+ext)
	}
	rpMatcherSet := caddy.ModuleMap{
		"path": caddyconfig.JSON(pathList, nil),
	}

	// create the final reverse proxy route which is
	// conditional on matching PHP files
	rpRoute := caddyhttp.Route{
		MatcherSetsRaw: []caddy.ModuleMap{rpMatcherSet},
		HandlersRaw:    []json.RawMessage{caddyconfig.JSONModuleObject(rpHandler, "handler", "reverse_proxy", nil)},
	}

	subroute := caddyhttp.Subroute{
		Routes: append(routes, rpRoute),
	}

	hostMatcherSet := caddy.ModuleMap{
		"host": caddyconfig.JSON(caddyhttp.MatchHost{fromAddr.Host}, nil),
	}

	server := &caddyhttp.Server{
		Routes: caddyhttp.RouteList{
			caddyhttp.Route{
				MatcherSetsRaw: []caddy.ModuleMap{hostMatcherSet},
				HandlersRaw: []json.RawMessage{caddyconfig.JSONModuleObject(subroute, "handler", "subroute", nil)},
			},
		},
		Listen: []string{":" + fromAddr.Port},
	}

	httpApp := caddyhttp.App{
		Servers: map[string]*caddyhttp.Server{"proxy": server},
	}

	cfg := &caddy.Config{
		Admin: &caddy.AdminConfig{Disabled: true},
		AppsRaw: caddy.ModuleMap{
			"http": caddyconfig.JSON(httpApp, nil),
		},
	}
	return fromAddr, toAddr, cfg, err
}

const (
	// DefaultFastCGIPort is the default port for FastCGI (PHP).
	DefaultFastCGIPort = 9000
)
