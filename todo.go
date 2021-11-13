// Copyright 2021 Changkun Ou. All rights reserved.
// Use of this source code is governed by a MIT
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/mailgun/mailgun-go/v4"
	"gopkg.in/yaml.v3"
)

type config struct {
	Person  string `yaml:"person"`
	Email   string `yaml:"email"`
	Domain  string `yaml:"domain"`
	APIKey  string `yaml:"apikey"`
	APIBase string `yaml:"apibase"`
	Inbox   string `yaml:"inbox"`
}

var (
	mg   *mailgun.MailgunImpl
	conf config

	//go:embed conf.yml
	confRaw []byte
)

func init() {
	log.SetPrefix("todo: ")
	log.SetFlags(0)

	err := yaml.Unmarshal(confRaw, &conf)
	if err != nil {
		log.Fatalf("cannot parse config, err: %v\n", err)
	}

	conf.APIKey = os.Getenv(conf.APIKey)
	if conf.APIKey == "" {
		log.Fatalf("missing mailgun API key from $MAILGUN_APIKEY")
	}

	mg = mailgun.NewMailgun(conf.Domain, conf.APIKey)
	mg.SetAPIBase(conf.APIBase)
}

func main() {
	flag.CommandLine.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: todo [ITEM]
> Further details.
>
SENT!

examples:
$ todo need to do something
$ todo "I've to do something"
`)
		flag.PrintDefaults()
	}
	flag.CommandLine.SetOutput(io.Discard)
	flag.Parse()

	subject := strings.Join(flag.Args(), " ")
	if subject == "" {
		log.Fatalf("missing todo subject.")
	}

	// When creating a TODO, future version may use different prefix to
	// filter emails on the email receiving side. Let's use it for now.
	a, err := newTODO("todo: " + subject)
	if err != nil {
		if errors.Is(err, errCanceled) {
			log.Println("TODO is canceled.")
			return
		}
		log.Fatalf("cannot created a TODO item: %v", err)
	}

	text := a.subject
	if len(a.text) != 0 {
		text = strings.Join(a.text, "\n")
	}

	for {
		err := sendEmail(context.Background(), a.subject, text, conf.Inbox)
		if err != nil {
			log.Println("failed to send email, retry in 3 seconds...")
			time.Sleep(3 * time.Second)
			continue
		}
		break
	}
	log.Println("SENT!")
}

func sendEmail(ctx context.Context, subject, text string, inbox string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	msg := mg.NewMessage(conf.Email, subject, text, inbox)
	_, _, err := mg.Send(ctx, msg)
	if err != nil {
		log.Printf("failed to send a TODO to %s: %v", conf.Person, err)
		return err
	}
	return nil
}

var errCanceled = errors.New("action canceled")

type todo struct {
	subject string
	text    []string
}

func newTODO(subject string) (*todo, error) {
	a := &todo{subject: subject}
	if !a.waitBody() {
		return nil, errCanceled
	}
	return a, nil
}

func (a *todo) waitBody() bool {
	s := bufio.NewScanner(os.Stdin)
	fmt.Println("(Enter an empty line to complete; Ctrl+C/Ctrl+D to cancel)")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)

	line := make(chan string, 1)
	go func() {
		for {
			fmt.Print("> ")
			if !s.Scan() {
				sigCh <- os.Interrupt
				return
			}
			l := s.Text()
			if len(l) == 0 {
				line <- ""
				return
			}
			line <- l
		}
	}()

	for {
		select {
		case <-sigCh:
			return false
		case l := <-line:
			if len(l) == 0 {
				return true
			}
			a.text = append(a.text, l)
		}
	}
}
