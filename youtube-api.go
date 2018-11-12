package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
	youtube "google.golang.org/api/youtube/v3"
)

const missingClientSecretsMessage = `
Please configure OAuth 2.0
`

type ClientConfig struct {
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	RedirectURIs []string `json:"redirect_uris"`
	AuthURI      string   `json:"auth_uri"`
	TokenURI     string   `json:"token_uri"`
}

type Config struct {
	Installed ClientConfig `json:"installed"`
	Web       ClientConfig `json:"web"`
}

type YoutubeClient struct {
	service *youtube.Service
}

func NewYoutubeClient() (*YoutubeClient, error) {
	client, err := buildOAuthHTTPClient(youtube.YoutubeForceSslScope)
	if err != nil {
		return nil, err
	}

	service, err := youtube.New(client)
	HandleError(err, "Error creating YouTube client")
	if err != nil {
		return nil, err
	}

	return &YoutubeClient{
		service: service,
	}, nil
}

// openURL opens a browser window to the specified location.
// This code originally appeared at:
//   http://stackoverflow.com/questions/10377243/how-can-i-launch-a-process-that-is-not-a-file-in-go
func openURL(url string) error {
	var err error
	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", "http://localhost:4001/").Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	default:
		err = fmt.Errorf("Cannot open URL %s on this platform", url)
	}
	return err
}

// readConfig reads the configuration from clientSecretsFile.
// It returns an oauth configuration object for use with the Google API client.
func readConfig(scope string) (*oauth2.Config, error) {
	// Read the secrets file
	data, err := ioutil.ReadFile(*clientSecretsFile)
	if err != nil {
		pwd, _ := os.Getwd()
		fullPath := filepath.Join(pwd, *clientSecretsFile)
		return nil, fmt.Errorf(missingClientSecretsMessage, fullPath)
	}

	cfg := new(Config)
	err = json.Unmarshal(data, &cfg)
	if err != nil {
		return nil, err
	}

	var redirectUri string
	if len(cfg.Web.RedirectURIs) > 0 {
		redirectUri = cfg.Web.RedirectURIs[0]
	} else if len(cfg.Installed.RedirectURIs) > 0 {
		redirectUri = cfg.Installed.RedirectURIs[0]
	} else {
		return nil, errors.New("Must specify a redirect URI in config file or when creating OAuth client")
	}

	return &oauth2.Config{
		ClientID:     cfg.Installed.ClientID,
		ClientSecret: cfg.Installed.ClientSecret,
		Scopes:       []string{scope},
		Endpoint:     oauth2.Endpoint{cfg.Installed.AuthURI, cfg.Installed.TokenURI},
		RedirectURL:  redirectUri,
	}, nil
}

// Start a web server that listens on http://localhost:8080.
// The webserver waits for an oauth code in the three-legged auth flow.
func startWebServer() (codeCh chan string, err error) {
	listener, err := net.Listen("tcp", "localhost:8080")
	if err != nil {
		return nil, err
	}
	codeCh = make(chan string)
	go http.Serve(listener, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		code := r.FormValue("code")
		codeCh <- code // send code to OAuth flow
		listener.Close()
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprintf(w, "Received code: %v\r\nYou can now safely close this browser window.", code)
	}))

	return codeCh, nil
}

// buildOAuthHTTPClient takes the user through the three-legged OAuth flow.
// It opens a browser in the native OS or outputs a URL, then blocks until
// the redirect completes to the /oauth2callback URI.
// It returns an instance of an HTTP client that can be passed to the
// constructor of the API client.
func buildOAuthHTTPClient(scope string) (*http.Client, error) {
	config, err := readConfig(scope)
	if err != nil {
		msg := fmt.Sprintf("Cannot read configuration file: %v", err)
		return nil, errors.New(msg)
	}

	ctx := context.Background()

	// Try to read the token from the cache file.
	// If an error occurs, do the three-legged OAuth flow because
	// the token is invalid or doesn't exist.
	var token *oauth2.Token

	data, err := ioutil.ReadFile(*cacheFile)
	if err == nil {
		err = json.Unmarshal(data, &token)
	}
	if (err != nil) || !token.Valid() {
		// Start web server.
		// This is how this program receives the authorization code
		// when the browser redirects.
		codeCh, err := startWebServer()
		if err != nil {
			return nil, err
		}
		fmt.Println(codeCh)

		// Open url in browser
		url := config.AuthCodeURL("")
		err = openURL(url)
		if err != nil {
			fmt.Println("Visit the URL below to get a code.",
				" This program will pause until the site is visted.")
		} else {
			fmt.Println("Your browser has been opened to an authorization URL.",
				" This program will resume once authorization has been provided.\n")

		}
		// Accept code on command line.
		fmt.Println(url)
		fmt.Print("Enter code: ")
		scanner := bufio.NewScanner(os.Stdin)
		code := ""
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Println(line)
			code = line
			break
		}

		// This code caches the authorization code on the local
		// filesystem, if necessary, as long as the TokenCache
		// attribute in the config is set.
		token, err = config.Exchange(ctx, code)
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(token)
		ioutil.WriteFile(*cacheFile, data, 0644)
	}

	return oauth2.NewClient(ctx, oauth2.StaticTokenSource(token)), nil
}

func HandleError(err error, message string) {
	if message == "" {
		message = "Error making API call"
	}
	if err != nil {
		log.Fatalf(message+": %v", err.Error())
	}
}

func addPropertyToResource(ref map[string]interface{}, keys []string, value string, count int) map[string]interface{} {
	for k := count; k < (len(keys) - 1); k++ {
		switch val := ref[keys[k]].(type) {
		case map[string]interface{}:
			ref[keys[k]] = addPropertyToResource(val, keys, value, (k + 1))
		case nil:
			next := make(map[string]interface{})
			ref[keys[k]] = addPropertyToResource(next, keys, value, (k + 1))
		}
	}
	// Only include properties that have values.
	if count == len(keys)-1 && value != "" {
		valueKey := keys[len(keys)-1]
		if valueKey[len(valueKey)-2:] == "[]" {
			ref[valueKey[0:len(valueKey)-2]] = strings.Split(value, ",")
		} else if len(valueKey) > 4 && valueKey[len(valueKey)-4:] == "|int" {
			ref[valueKey[0:len(valueKey)-4]], _ = strconv.Atoi(value)
		} else if value == "true" {
			ref[valueKey] = true
		} else if value == "false" {
			ref[valueKey] = false
		} else {
			ref[valueKey] = value
		}
	}
	return ref
}

func createResource(properties map[string]string) string {
	resource := make(map[string]interface{})
	for key, value := range properties {
		keys := strings.Split(key, ".")
		ref := addPropertyToResource(resource, keys, value, 0)
		resource = ref
	}
	propJson, err := json.Marshal(resource)
	if err != nil {
		log.Fatal("cannot encode to JSON ", err)
	}
	return string(propJson)
}

func (client *YoutubeClient) FindPlaylist(name string) (string, error) {
	call := client.service.Playlists.List("snippet")
	call = call.Mine(true).MaxResults(50)
	response, err := call.Do()
	if err != nil {
		return "", err
	}
	for _, item := range response.Items {
		if item.Snippet.Title == name {
			return item.Id, nil
		}
	}
	return "", nil
}

func (client *YoutubeClient) CreatePlaylist(name, description string) (string, error) {
	properties := (map[string]string{
		"snippet.title":       "Discord Bot Test",
		"snippet.description": "A playlist for all videos from a Discord channel",
	})
	res := createResource(properties)

	resource := &youtube.Playlist{}
	if err := json.NewDecoder(strings.NewReader(res)).Decode(&resource); err != nil {
		return "", err
	}
	call := client.service.Playlists.Insert("snippet,status", resource)
	response, err := call.Do()
	if err != nil {
		return "", err
	}
	fmt.Println(response)
	return response.Id, nil
}

func (client *YoutubeClient) AddToPlaylist(videoId, playlistId string) error {
	properties := (map[string]string{
		"snippet.playlistId":         playlistId,
		"snippet.resourceId.kind":    "youtube#video",
		"snippet.resourceId.videoId": videoId,
	})
	res := createResource(properties)

	resource := &youtube.PlaylistItem{}
	if err := json.NewDecoder(strings.NewReader(res)).Decode(&resource); err != nil {
		return err
	}
	call := client.service.PlaylistItems.Insert("snippet", resource)
	_, err := call.Do()
	return err
}
