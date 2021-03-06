package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	log "github.com/sirupsen/logrus"
	kingpin "gopkg.in/alecthomas/kingpin.v2"

	"github.com/9seconds/ddoctor/internal/checkers"
	"github.com/9seconds/ddoctor/internal/config"
	"github.com/9seconds/ddoctor/internal/presenter"
	"github.com/9seconds/ddoctor/internal/server"
)

var (
	app = kingpin.New(
		"ddoctor",
		"Docker doctor - checking health of your containers.")

	debug = app.Flag("debug", "Run in debug mode.").
		Short('d').
		Envar("DDOCTOR_DEBUG").
		Bool()
	oneShot = app.Flag("one-shot", "Do not run forever, execute only once").
		Short('o').
		Bool()

	configFile = app.Arg("config-path", "Path to the config").
			Required().
			File()
)

func init() {
	app.Version("0.0.1")
	log.SetFormatter(&log.TextFormatter{})
	log.SetLevel(log.WarnLevel)
}

func main() {
	app.Parse(os.Args[1:])

	if *debug {
		log.SetLevel(log.DebugLevel)
	}

	cf, err := config.ParseConfigFile(*configFile)
	if err != nil {
		log.Fatalf(err.Error())
	}

	log.WithFields(log.Fields{
		"periodicity": cf.Periodicity.Duration,
		"host":        cf.Host,
		"port":        cf.Port,
		"ok_status":   cf.OkStatus,
		"nok_status":  cf.NokStatus,
	}).Info("Config file")
	for _, v := range cf.Checks {
		log.WithFields(log.Fields{
			"type":         v.Type,
			"url":          v.URL.URL,
			"exec":         v.Exec,
			"timeout":      v.Timeout.Duration,
			"status_codes": v.StatusCodes,
		}).Info("Check")
	}

	checks := make([]checkers.Checker, 0, len(cf.Checks))
	for _, value := range cf.Checks {
		var instance checkers.Checker
		var err error

		switch value.Type {
		case "shell":
			instance, err = checkers.NewShellChecker(value.Timeout.Duration, value.Exec)
		case "command":
			instance, err = checkers.NewCommandChecker(value.Timeout.Duration, value.Exec)
		case "network":
			instance, err = checkers.NewNetworkChecker(value.Timeout.Duration, value.URL.URL, value.StatusCodes)
		}

		if err != nil {
			log.Fatalf(err.Error())
		}

		checks = append(checks, instance)
	}

	rootCtx, rootCancel := context.WithCancel(context.Background())
	signalChannel := make(chan os.Signal, 1)
	signal.Notify(signalChannel,
		syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)

	go func() {
		for sig := range signalChannel {
			log.WithField("signo", sig).Info("Caught exit signal")
			rootCancel()
		}
	}()

	if *oneShot {
		oneShotVersion(rootCtx, checks)
	} else {
		server.Serve(cf, rootCtx, checks)
	}
}

func oneShotVersion(ctx context.Context, checks []checkers.Checker) {
	channel := make(chan *checkers.CheckResult, len(checks))

	for _, v := range checks {
		go v.Run(ctx, channel)
	}

	exitCode := 0
	results := make([]*checkers.CheckResult, len(checks))
	for i := 0; i < len(checks); i++ {
		results[i] = <-channel
		if results[i].Ok {
			exitCode = 2
		}
	}
	close(channel)

	serialized, err := presenter.Serialize(results, true)
	if err != nil {
		log.Fatalf("Cannot serialize to JSON: %s", err.Error())
	}

	fmt.Println(string(serialized))
	os.Exit(exitCode)
}
