package main

import (
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/bwmarrin/discordgo"
	log "github.com/sirupsen/logrus"
	"mvdan.cc/xurls"
)

var (
	clientSecretsFile = flag.String("secrets", "cs.json", "Client Secrets configuration")
	cacheFile         = flag.String("cache", "request.token", "Token cache file")

	ytClient   *YoutubeClient
	playlistID string
)

func init() {
	// Output to stdout instead of the default stderr
	// Can be any io.Writer, see below for File example
	log.SetOutput(os.Stdout)
}

func main() {

	var err error
	ytClient, err = NewYoutubeClient()

	playlistID, err = ytClient.FindPlaylist("Discord Bot Test")
	if err != nil {
		log.Fatal(err.Error())
	}

	if playlistID == "" {
		log.Info("Playlist not found... Creating a new playlist")
		playlistID, err = ytClient.CreatePlaylist("Discord Bot Test", "A simple playlist for Discord Bot testing")
	}
	log.Infof("Playlist '%s' found with Id %s\n", "Discord Bot Test", playlistID)

	token := "..."
	discord, err := discordgo.New(fmt.Sprintf("Bot %s", token))
	if err != nil {
		panic(err)
	}

	discord.AddHandler(messageCreated)

	err = discord.Open()
	if err != nil {
		panic(err)
	}

	fmt.Println("Bot is running. Press CTRL-C to exit.")
	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
	<-sc

	// Cleanly close down the Discord session.
	discord.Close()
}

func messageCreated(s *discordgo.Session, m *discordgo.MessageCreate) {

	// Ignore all messages created by the bot itself
	// This isn't required in this specific example but it's a good practice.
	if m.Author.ID == s.State.User.ID {
		return
	}

	urls := xurls.Strict().FindAllString(m.Content, -1)
	ids := make([]string, 0)
	if len(urls) > 0 {
		for _, urlStr := range urls {
			url, err := url.Parse(urlStr)
			if err != nil {
				fmt.Println(err.Error())
			}
			if url.Hostname() == "youtube.com" || url.Hostname() == "www.youtube.com" {
				id := url.Query().Get("v")
				ids = append(ids, id)
			} else if url.Hostname() == "youtu.be" {
				id := strings.Split(url.EscapedPath(), "/")[1]
				ids = append(ids, id)
			}
		}
	}

	for _, id := range ids {
		log.Debugf("Adding video '%s' to playlist '%s'", id, playlistID)
		err := ytClient.AddToPlaylist(id, playlistID)
		if err != nil {
			log.Warn(err.Error())
		}
	}

	// var message string
	// if len(ids) > 0 {
	// 	message = fmt.Sprintf("If found Youtube videos in the message: %s", strings.Join(ids, ", "))
	// }

	// s.ChannelMessageSend(m.ChannelID, message)
}
