package cmd

import (
	"context"
	"errors"
	"github.com/predakanga/external-dns-configmap-provider/pkg"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/client-go/util/homedir"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sigs.k8s.io/external-dns/endpoint"
	"time"
)

const baseLogLevel = log.InfoLevel

var kubeConfig, targetNamespace, targetName, listenAddress string
var verbosity int
var regexDomainFilter, regexDomainExclusion string
var domainFilter, excludeDomains []string
var allowWildcards bool

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   os.Args[0],
	Short: "External DNS -> ConfigMap webhook",

	Run: func(cmd *cobra.Command, args []string) {
		// Bump up the log level if requested
		desiredLevel := baseLogLevel
		if verbosity > 0 {
			desiredLevel = baseLogLevel + log.Level(verbosity)
			if desiredLevel > log.TraceLevel {
				desiredLevel = log.TraceLevel
			}
		}
		log.SetLevel(desiredLevel)
		log.Infof("Log level: %v", desiredLevel)

		// And move on to validation
		if targetName == "" {
			log.Fatal("You must specify a name with --output")
		}

		// Domain filter code pulled from external-dns
		var domainFilterObj endpoint.DomainFilter
		if regexDomainFilter != "" {
			domainFilterObj = endpoint.NewRegexDomainFilter(regexp.MustCompile(regexDomainFilter), regexp.MustCompile(regexDomainExclusion))
		} else {
			domainFilterObj = endpoint.NewDomainFilterWithExclusions(domainFilter, excludeDomains)
		}

		// Create the web server
		storage := pkg.NewStorage(targetName, targetNamespace, kubeConfig)
		handler := pkg.NewProvider(domainFilterObj, storage, allowWildcards)
		server := http.Server{
			Addr:    listenAddress,
			Handler: handler,
		}

		// Signal handler to shut down gracefully
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt)

		go func() {
			<-sigChan
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			if err := server.Shutdown(ctx); err != nil {
				os.Exit(0)
			}
		}()

		if err := server.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			log.WithError(err).Fatal("Error encountered")
		}
	},
}

func Execute(version string) {
	rootCmd.Version = version
	cobra.CheckErr(rootCmd.Execute())
}

func init() {
	if home := homedir.HomeDir(); home != "" {
		rootCmd.PersistentFlags().StringVar(&kubeConfig, "kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		rootCmd.PersistentFlags().StringVar(&kubeConfig, "kubeconfig", "", "absolute path to the kubeconfig file")
	}
	rootCmd.PersistentFlags().CountVarP(&verbosity, "verbose", "v", "increase log verbosity")
	rootCmd.Flags().StringVarP(&targetNamespace, "namespace", "n", "default", "namespace for the managed ConfigMap")
	rootCmd.Flags().StringVarP(&targetName, "output", "o", "", "desired ConfigMap name")
	rootCmd.Flags().StringVarP(&listenAddress, "listen", "l", ":8080", "[address]:[port] to listen on")
	_ = rootCmd.MarkFlagRequired("output")

	rootCmd.Flags().StringArrayVar(&domainFilter, "domain-filter", []string{}, "Limit possible target zones by a domain suffix; specify multiple times for multiple domains (optional)")
	rootCmd.Flags().StringArrayVar(&excludeDomains, "exclude-domains", []string{}, "Exclude subdomains (optional)")
	rootCmd.Flags().StringVar(&regexDomainFilter, "regex-domain-filter", "", "Limit possible domains and target zones by a Regex filter; Overrides domain-filter (optional)")
	rootCmd.Flags().StringVar(&regexDomainExclusion, "regex-domain-exclusion", "", "Regex filter that excludes domains and target zones matched by regex-domain-filter (optional)")

	rootCmd.Flags().BoolVar(&allowWildcards, "allow-wildcards", false, "Allow wildcard entries (please ensure there is no overlap between entries)")
}
