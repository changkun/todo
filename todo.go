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
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/mailgun/mailgun-go/v4"
	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/option"
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
	mg     *mailgun.MailgunImpl
	conf   config
	client openai.Client

	//go:embed conf.yml
	confRaw []byte
)

func init() {
	err := yaml.Unmarshal(confRaw, &conf)
	if err != nil {
		fatal("todo: cannot parse config, err: %v\n", err)
	}

	conf.APIKey = os.Getenv(conf.APIKey)
	if conf.APIKey == "" {
		fatal("todo: missing mailgun API key from $MAILGUN_APIKEY")
	}

	mg = mailgun.NewMailgun(conf.Domain, conf.APIKey)
	mg.SetAPIBase(conf.APIBase)

	openaiToken := os.Getenv("OPENAI_API_KEY")
	if openaiToken == "" {
		return
	}

	client = openai.NewClient(option.WithAPIKey(openaiToken))
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
		fatal("todo: missing todo subject.")
	}

	// When creating a TODO, future version may use different prefix to
	// filter emails on the email receiving side. Let's use it for now.
	a, err := newTODO("todo: " + subject)
	if err != nil {
		if errors.Is(err, errCanceled) {
			fmt.Fprintf(os.Stderr, "todo: TODO is canceled.")
			return
		}
		fatal("todo: cannot created a TODO item: %v", err)
	}

	text := a.subject
	if len(a.text) != 0 {
		text = strings.Join(a.text, "\n")
	}

	fmt.Fprintf(os.Stdout, "todo: generating GPT suggestion...\n")
	resp, err := client.Chat.Completions.New(
		context.Background(),
		openai.ChatCompletionNewParams{
			Model: openai.ChatModelGPT4_1Nano2025_04_14,
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.SystemMessage("You are a helpful assistant that helps summarize the given text."),
				openai.UserMessage(text),
			},
		},
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "todo: failed to generate GPT suggestion: %v", err)
	} else {
		text += "\n\nSuggestion by GPT4:\n" + resp.Choices[0].Message.Content + "\n"
	}

	for {
		err := sendEmail(context.Background(), a.subject, text, conf.Inbox)
		if err != nil {
			fmt.Fprintf(os.Stderr, "todo: failed to send email, retry in 3 seconds...")
			time.Sleep(3 * time.Second)
			continue
		}
		break
	}
	fmt.Fprintf(os.Stdout, "\n todo: SENT!")
}

func sendEmail(ctx context.Context, subject, text string, inbox string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	msg := mg.NewMessage(conf.Email, subject, text, inbox)
	_, _, err := mg.Send(ctx, msg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "todo: failed to send a TODO to %s: %v", conf.Person, err)
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
	fmt.Fprintf(os.Stdout, "todo: (Enter an empty line to complete; Ctrl+C/Ctrl+D to cancel)\n")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)

	line := make(chan string, 1)
	go func() {
		for {
			fmt.Fprintf(os.Stdout, "> ")
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

func fatal(msg string, args ...any) {
	fmt.Fprintf(os.Stderr, msg, args...)
	os.Exit(1)
}
