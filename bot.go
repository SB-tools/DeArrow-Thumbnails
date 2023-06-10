package main

import (
	"context"
	"fmt"
	"github.com/disgoorg/disgo"
	"github.com/disgoorg/disgo/bot"
	"github.com/disgoorg/disgo/cache"
	"github.com/disgoorg/disgo/discord"
	"github.com/disgoorg/disgo/events"
	"github.com/disgoorg/disgo/gateway"
	"github.com/disgoorg/json"
	"github.com/disgoorg/log"
	"github.com/disgoorg/snowflake/v2"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
)

const (
	dearrowApiURL          = "https://sponsor.ajay.app/api/branding?videoID=%s"
	dearrowThumbnailApiURL = "https://dearrow-thumb.ajay.app/api/v1/getThumbnail?videoID=%s&time=%f&generateNow=true"
)

func main() {
	log.SetLevel(log.LevelInfo)
	log.Info("starting the bot...")
	log.Info("disgo version: ", disgo.Version)

	client, err := disgo.New(os.Getenv("DEARROW_BOT_TOKEN"),
		bot.WithGatewayConfigOpts(gateway.WithIntents(gateway.IntentGuildMessages, gateway.IntentMessageContent, gateway.IntentGuilds),
			gateway.WithPresenceOpts(gateway.WithWatchingActivity("YouTube embeds"))),
		bot.WithCacheConfigOpts(cache.WithCaches(cache.FlagChannels, cache.FlagRoles, cache.FlagMembers),
			cache.WithMemberCachePolicy(func(entity discord.Member) bool {
				return entity.User.ID == snowflake.GetEnv("DEARROW_USER_ID")
			})),
		bot.WithEventListeners(&events.ListenerAdapter{
			OnGuildMessageCreate: func(event *events.GuildMessageCreate) {
				replaceYouTubeEmbed(event.GenericGuildMessage)
			},
			OnGuildMessageUpdate: func(event *events.GuildMessageUpdate) {
				replaceYouTubeEmbed(event.GenericGuildMessage)
			},
		}))
	if err != nil {
		log.Fatal("error while building disgo instance: ", err)
	}

	defer client.Close(context.TODO())

	if err := client.OpenGateway(context.TODO()); err != nil {
		log.Fatal("error while connecting to the gateway: ", err)
	}

	log.Info("dearrow bot is now running.")
	s := make(chan os.Signal, 1)
	signal.Notify(s, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-s
}

func replaceYouTubeEmbed(event *events.GenericGuildMessage) {
	channel, _ := event.Channel()
	client := event.Client()
	caches := client.Caches()
	selfMember, _ := caches.SelfMember(event.GuildID)
	permissions := caches.MemberPermissionsInChannel(channel, selfMember)
	if permissions.Missing(discord.PermissionSendMessages) {
		return
	}
	message := event.Message
	embeds := message.Embeds
	if len(embeds) == 0 {
		return
	}
	var embed discord.Embed
	for _, e := range embeds {
		provider := e.Provider
		if provider != nil && provider.Name == "YouTube" {
			embed = e
			break
		}
		return
	}
	u, _ := url.Parse(embed.URL)
	videoID := u.Query().Get("v")
	path := fmt.Sprintf(dearrowApiURL, videoID)
	rs, err := http.Get(path)
	if err != nil {
		log.Errorf("there was an error while running a branding request (%s): ", path, err)
		return
	}
	defer rs.Body.Close()
	var brandingResponse BrandingResponse
	if err = json.NewDecoder(rs.Body).Decode(&brandingResponse); err != nil {
		log.Errorf("there was an error while decoding a branding response (%d %s): ", rs.StatusCode, path, err)
		return
	}
	author := embed.Author
	titles := brandingResponse.Titles
	thumbnails := brandingResponse.Thumbnails
	title := embed.Title
	thumbnailURL := embed.Thumbnail.URL
	embedBuilder := discord.NewEmbedBuilder()
	embedBuilder.SetColor(embed.Color)
	embedBuilder.SetAuthor(author.Name, author.URL, author.IconURL)
	embedBuilder.SetURL(embed.URL)
	if len(titles) != 0 {
		title = titles[0].Title
		embedBuilder.SetFooterText("Original title: " + embed.Title)
	}
	embedBuilder.SetTitle(title)
	if len(thumbnails) != 0 && !thumbnails[0].Original {
		thumbnailURL = formatThumbnailURL(videoID, thumbnails[0].Timestamp)
	} else {
		videoDuration := brandingResponse.VideoDuration
		if videoDuration != nil && *videoDuration != 0 {
			duration := *videoDuration
			thumbnailURL = formatThumbnailURL(videoID, brandingResponse.RandomTime*duration)
		}
	}
	embedBuilder.SetImage(thumbnailURL)
	rest := client.Rest()
	channelID := event.ChannelID
	messageID := event.MessageID
	_, err = rest.CreateMessage(channelID, discord.NewMessageCreateBuilder().
		SetEmbeds(embedBuilder.Build()).
		SetMessageReferenceByID(messageID).
		SetAllowedMentions(&discord.AllowedMentions{}).
		Build())
	if err != nil {
		log.Error("there was an error while creating a message: ", err)
		return
	}
	_, err = rest.UpdateMessage(channelID, messageID, discord.NewMessageUpdateBuilder().
		SetSuppressEmbeds(true).
		Build())
	if err != nil {
		log.Error("there was an error while suppressing embeds: ", err)
	}
}

type BrandingResponse struct {
	Titles []struct {
		Title string `json:"title"`
	} `json:"titles"`
	Thumbnails []struct {
		Timestamp float64 `json:"timestamp"`
		Original  bool    `json:"original"`
	} `json:"thumbnails"`
	RandomTime    float64  `json:"randomTime"`
	VideoDuration *float64 `json:"videoDuration"`
}

func formatThumbnailURL(videoID string, timestamp float64) string {
	return fmt.Sprintf(dearrowThumbnailApiURL, videoID, timestamp)
}
