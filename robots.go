package robots

import (
	"context"
	"fmt"
	"log"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

type SlackAppMentionHandler interface {
	HandleAppMention(ctx context.Context, ev *slackevents.AppMentionEvent) error
}

type SlackMessageHandler interface {
	HandleMessage(ctx context.Context, ev *slackevents.MessageEvent) error
}

type SlackSlashCommandHandler interface {
	HandleSlashCommand(ctx context.Context, cmd string) ([]slack.Block, error)
}

type SlackBlockActionHandler interface {
	HandleBlockAction(ctx context.Context, action, value string, callback slack.InteractionCallback) error
}

type SlackViewSubmissionHandler interface {
	HandleViewSubmission(ctx context.Context, action, value, privateMetadata string, callback slack.InteractionCallback) error
}

type SlackBot struct {
	*slack.Client
	Handler any
	Socket  *socketmode.Client
}

// Run starts the bot.
func (b *SlackBot) Run(ctx context.Context) {
	// TODO(chris): How do we gracefully shutdown the socket?
	go b.Socket.Run()

	for evt := range b.Socket.Events {
		switch evt.Type {
		case socketmode.EventTypeEventsAPI:
			eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
			if !ok {
				log.Printf("Unexpected data: %v", evt.Data)

				continue
			}
			b.Socket.Ack(*evt.Request)

			switch ev := eventsAPIEvent.InnerEvent.Data.(type) {
			case *slackevents.AppMentionEvent:
				if handler, ok := b.Handler.(SlackAppMentionHandler); ok {
					//log.Printf("⭐ app mention handler: %s", ev.Text)
					if err := handler.HandleAppMention(ctx, ev); err != nil {
						b.Reply(ev.Channel, ev.TimeStamp, slack.MsgOptionBlocks(
							errorBlock(fmt.Sprintf(":warning: Error! `%s`: %v", ev.Text, err)),
						))
					}
				}

			case *slackevents.MessageEvent:
				if handler, ok := b.Handler.(SlackMessageHandler); ok {
					//log.Printf("⭐ message handler: %s", ev.Text)
					if err := handler.HandleMessage(ctx, ev); err != nil {
						b.Reply(ev.Channel, ev.TimeStamp, slack.MsgOptionBlocks(
							errorBlock(fmt.Sprintf(":warning: Error! `%s`: %v", ev.Text, err)),
						))
					}
				}
			}

		case socketmode.EventTypeSlashCommand:
			cmd, ok := evt.Data.(slack.SlashCommand)
			if !ok {
				log.Printf("Ignored %+v\n", evt)
				b.Socket.Ack(*evt.Request)
				continue
			}

			if handler, ok := b.Handler.(SlackSlashCommandHandler); ok {
				blocks, err := handler.HandleSlashCommand(ctx, cmd.Command)
				if err != nil {
					b.Socket.Ack(*evt.Request, map[string]interface{}{
						"blocks": []slack.Block{
							errorBlock(fmt.Sprintf(":warning: Error! `%s`: %v", cmd.Command, err)),
						},
					})
				}

				b.Socket.Ack(*evt.Request, map[string]interface{}{
					"blocks": blocks,
				})
			}

		case socketmode.EventTypeInteractive:
			callback, ok := evt.Data.(slack.InteractionCallback)
			if !ok {
				log.Printf("Unexpected data: %v", evt.Data)
				continue
			}
			b.Socket.Ack(*evt.Request)

			switch callback.Type {
			case slack.InteractionTypeBlockActions:
				for _, action := range callback.ActionCallback.BlockActions {
					log.Printf("button pushed: %s %s", action.ActionID, action.Value)
					if handler, ok := b.Handler.(SlackBlockActionHandler); ok {
						if err := handler.HandleBlockAction(ctx, action.ActionID, action.Value, callback); err != nil {
							b.Reply(callback.Channel.ID, callback.MessageTs, slack.MsgOptionBlocks(
								errorBlock(fmt.Sprintf(":warning: Error! `%s`: %v", action.ActionID, err)),
							))
						}
					}
				}

			case slack.InteractionTypeViewSubmission:
				inputs := callback.View.State.Values
				for _, input := range inputs {
					for actionID, value := range input {
						if handler, ok := b.Handler.(SlackViewSubmissionHandler); ok {
							if err := handler.HandleViewSubmission(ctx, actionID, value.Value, callback.View.PrivateMetadata, callback); err != nil {
								b.Reply(callback.Channel.ID, callback.MessageTs, slack.MsgOptionBlocks(
									errorBlock(fmt.Sprintf(":warning: Error! `%s`: %v", actionID, err)),
								))
							}
						}
					}
				}
			}
		}
	}
}

func (b *SlackBot) Reply(channel string, ts string, opts ...slack.MsgOption) error {
	_, _, err := b.PostMessage(
		channel,
		append(opts, slack.MsgOptionTS(ts))...)
	return err
}

func errorBlock(msg string) *slack.SectionBlock {
	return slack.NewSectionBlock(
		&slack.TextBlockObject{
			Type: slack.MarkdownType,
			Text: msg,
		},
		nil,
		nil,
	)
}
