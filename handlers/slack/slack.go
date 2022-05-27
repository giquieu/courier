package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/buger/jsonparser"
	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/courier/utils"
	"github.com/nyaruka/gocommon/urns"
	"github.com/pkg/errors"
)

var apiURL = "https://slack.com/api"

const (
	configBotToken        = "bot_token"
	configUserToken       = "user_token"
	configValidationToken = "verification_token"
)

var (
	ErrAlreadyPublic         = "already_public"
	ErrPublicVideoNotAllowed = "public_video_not_allowed"
)

func init() {
	courier.RegisterHandler(newHandler())
}

type handler struct {
	handlers.BaseHandler
}

func newHandler() courier.ChannelHandler {
	return &handler{handlers.NewBaseHandler(courier.ChannelType("SL"), "Slack")}
}

func (h *handler) Initialize(s courier.Server) error {
	h.SetServer(s)
	s.AddHandlerRoute(h, http.MethodPost, "receive", h.receiveEvent)
	return nil
}

func handleURLVerification(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request, payload *moPayload) ([]courier.Event, error) {
	validationToken := channel.ConfigForKey(configValidationToken, "")
	if validationToken != payload.Token {
		w.WriteHeader(http.StatusForbidden)
		return nil, fmt.Errorf("Wrong validation token for channel: %s", channel.UUID())
	}
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(payload.Challenge))
	return nil, nil
}

func (h *handler) receiveEvent(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	payload := &moPayload{}
	err := handlers.DecodeAndValidateJSON(payload, r)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	if payload.Type == "url_verification" {
		return handleURLVerification(ctx, channel, w, r, payload)
	}

	// if event is not a message or is from the bot ignore it
	if strings.Contains(payload.Event.Type, "message") && payload.Event.BotID == "" {

		date := time.Unix(int64(payload.EventTime), 0)

		var userName string
		var path string
		if payload.Event.ChannelType == "channel" {
			path = payload.Event.Channel
		} else if payload.Event.ChannelType == "im" {
			path = payload.Event.User
			userInfo, err := h.GetUserInfo(payload.Event.User, channel)
			if err != nil {
				return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
			}
			userName = userInfo.User.RealName
		}

		urn, err := urns.NewURNFromParts(urns.SlackScheme, path, "", userName)
		if err != nil {
			return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
		}

		attachmentURLs := make([]string, 0)
		for _, file := range payload.Event.Files {
			fileURL, err := h.resolveFile(ctx, channel, file)
			if err != nil {
				courier.LogRequestError(r, channel, err)
			} else {
				attachmentURLs = append(attachmentURLs, fileURL)
			}
		}

		text := payload.Event.Text
		msg := h.Backend().NewIncomingMsg(channel, urn, text).WithReceivedOn(date).WithExternalID(payload.EventID).WithContactName(userName)

		for _, attURL := range attachmentURLs {
			msg.WithAttachment(attURL)
		}

		return handlers.WriteMsgsAndResponse(ctx, h, []courier.Msg{msg}, w, r)
	}
	return nil, handlers.WriteAndLogRequestIgnored(ctx, h, channel, w, r, "Ignoring request, no message")
}

func (h *handler) resolveFile(ctx context.Context, channel courier.Channel, file File) (string, error) {
	userToken := channel.StringConfigForKey(configUserToken, "")

	fileApiURL := apiURL + "/files.sharedPublicURL"

	data := strings.NewReader(fmt.Sprintf(`{"file":"%s"}`, file.ID))
	req, err := http.NewRequest(http.MethodPost, fileApiURL, data)
	if err != nil {
		courier.LogRequestError(req, channel, err)
		return "", err
	}
	req.Header.Add("Content-Type", "application/json; charset=utf-8")
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", userToken))

	rr, err := utils.MakeHTTPRequest(req)
	if err != nil {
		log := courier.NewChannelLogFromRR("File Resolving", channel, courier.NilMsgID, rr).WithError("File Resolving Error", err)
		h.Backend().WriteChannelLogs(ctx, []*courier.ChannelLog{log})
		return "", err
	}

	var fResponse fileResponse
	if err := json.Unmarshal([]byte(rr.Body), &fResponse); err != nil {
		return "", errors.Errorf("couldn't unmarshal file response: %v", err)
	}

	currentFile := fResponse.File

	if !fResponse.OK {
		if fResponse.Error != ErrAlreadyPublic {
			if fResponse.Error == ErrPublicVideoNotAllowed {
				return "", errors.Errorf("public sharing of videos is not available for a free instance of Slack. file id: %s. error: %s", file.ID, fResponse.Error)
			}
			return "", errors.Errorf("couldn't resolve file for file id: %s. error: %s", file.ID, fResponse.Error)
		}
		currentFile = file
	}

	pubLnkSplited := strings.Split(currentFile.PermalinkPublic, "-")
	pubSecret := pubLnkSplited[len(pubLnkSplited)-1]
	filePath := currentFile.URLPrivateDownload + "?pub_secret=" + pubSecret

	return filePath, nil
}

func (h *handler) SendMsg(ctx context.Context, msg courier.Msg) (courier.MsgStatus, error) {
	botToken := msg.Channel().StringConfigForKey(configBotToken, "")
	if botToken == "" {
		return nil, fmt.Errorf("missing bot token for SL/slack channel")
	}
	sendURL := apiURL + "/chat.postMessage"

	status := h.Backend().NewMsgStatusForID(msg.Channel(), msg.ID(), courier.MsgErrored)

	if msg.Text() != "" {
		msgPayload := &mtPayload{
			Channel: msg.URN().Path(),
			Text:    msg.Text(),
		}

		body, err := json.Marshal(msgPayload)
		if err != nil {
			return status, err
		}

		req, err := http.NewRequest(http.MethodPost, sendURL, bytes.NewReader(body))
		if err != nil {
			return status, err
		}
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", botToken))

		rr, err := utils.MakeHTTPRequest(req)

		log := courier.NewChannelLogFromRR("Message Sent", msg.Channel(), msg.ID(), rr).WithError("Message Send Error", err)
		status.AddLog(log)

		ok, err := jsonparser.GetBoolean([]byte(rr.Body), "ok")
		if err != nil || !ok {
			return status, err
		}
	}

	if len(msg.Attachments()) > 0 {
		fileAttachments := []FileParams{}
		for _, attachment := range msg.Attachments() {
			_, attURL := handlers.SplitAttachment(attachment)

			req, err := http.NewRequest(http.MethodGet, attURL, nil)
			if err != nil {
				return status, errors.Wrapf(err, "error building file request")
			}
			resp, err := utils.MakeHTTPRequest(req)
			log := courier.NewChannelLogFromRR("Fetching media", msg.Channel(), msg.ID(), resp).WithError("error fetching media", err)
			status.AddLog(log)
			if err != nil {
				return status, err
			}
			filename, err := utils.BasePathForURL(attURL)
			if err != nil {
				return status, err
			}
			fileAttachments = append(fileAttachments, FileParams{
				File:     resp.Body,
				FileName: filename,
				Channels: msg.URN().Path(),
			})
		}

		for _, attParams := range fileAttachments {
			uploadURL := apiURL + "/files.upload"

			body := &bytes.Buffer{}
			writer := multipart.NewWriter(body)
			mediaPart, err := writer.CreateFormFile("file", attParams.FileName)
			if err != nil {
				return status, errors.Wrapf(err, "failed to create file form field")
			}
			io.Copy(mediaPart, bytes.NewReader(attParams.File))

			filenamePart, err := writer.CreateFormField("filename")
			if err != nil {
				return status, errors.Wrapf(err, "failed to create filename form field")
			}
			io.Copy(filenamePart, strings.NewReader(attParams.FileName))

			channelsPart, err := writer.CreateFormField("channels")
			if err != nil {
				return status, errors.Wrapf(err, "failed to create channels form field")
			}
			io.Copy(channelsPart, strings.NewReader(attParams.Channels))

			writer.Close()

			req, err := http.NewRequest(http.MethodPost, uploadURL, bytes.NewReader(body.Bytes()))
			if err != nil {
				return status, errors.Wrapf(err, "error building request to file upload endpoint")
			}
			req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", botToken))
			req.Header.Add("Content-Type", writer.FormDataContentType())
			resp, err := utils.MakeHTTPRequest(req)
			if err != nil {
				return status, errors.Wrapf(err, "error uploading file to slack")
			}
			{
				var fResponse fileResponse
				if err := json.Unmarshal([]byte(resp.Body), &fResponse); err != nil {
					return status, errors.Errorf("couldn't unmarshal file response: %v", err)
				}

				if !fResponse.OK {
					return status, errors.Errorf("error uploading file to slack: %s.", fResponse.Error)
				}
			}
			log := courier.NewChannelLogFromRR("uploading file to Slack", msg.Channel(), msg.ID(), resp).WithError("Error uploading file to Slack", err)

			fmt.Println("Video uploaded to slack")
			status.AddLog(log)
		}
	}

	status.SetStatus(courier.MsgWired)

	return status, nil
}

func (h *handler) sendMsgPart(msg courier.Msg, token string, form url.Values) (string, *courier.ChannelLog, error) {
	return "", nil, nil
}

func (h handler) GetUserInfo(userSlackID string, channel courier.Channel) (*UserInfo, error) {
	resource := "/users.info"
	urlStr := apiURL + resource

	req, err := http.NewRequest(http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Add("Authorization", "Bearer "+channel.StringConfigForKey(configBotToken, ""))

	q := req.URL.Query()
	q.Add("user", userSlackID)
	req.URL.RawQuery = q.Encode()

	rr, err := utils.MakeHTTPRequest(req)
	if err != nil {
		log := courier.NewChannelLogFromRR("Get User info", channel, courier.NilMsgID, rr).WithError("Request User Info Error", err)
		h.Backend().WriteChannelLogs(context.TODO(), []*courier.ChannelLog{log})
		return nil, err
	}

	var uInfo *UserInfo
	if err := json.Unmarshal(rr.Body, &uInfo); err != nil {
		log := courier.NewChannelLogFromRR("Get User info", channel, courier.NilMsgID, rr).WithError("Unmarshal User Info Error", err)
		h.Backend().WriteChannelLogs(context.TODO(), []*courier.ChannelLog{log})
		return nil, err
	}

	return uInfo, nil
}

type mtPayload struct {
	Channel string `json:"channel"`
	Text    string `json:"text"`
}

type moPayload struct {
	Token    string `json:"token,omitempty"`
	TeamID   string `json:"team_id,omitempty"`
	APIAppID string `json:"api_app_id,omitempty"`
	Event    struct {
		Type        string `json:"type,omitempty"`
		Channel     string `json:"channel,omitempty"`
		User        string `json:"user,omitempty"`
		Text        string `json:"text,omitempty"`
		Ts          string `json:"ts,omitempty"`
		EventTs     string `json:"event_ts,omitempty"`
		ChannelType string `json:"channel_type,omitempty"`
		Files       []File `json:"files"`
		BotID       string `json:"bot_id,omitempty"`
	} `json:"event,omitempty"`
	Type           string   `json:"type,omitempty"`
	AuthedUsers    []string `json:"authed_users,omitempty"`
	AuthedTeams    []string `json:"authed_teams,omitempty"`
	Authorizations []struct {
		EnterpriseID string `json:"enterprise_id,omitempty"`
		TeamID       string `json:"team_id,omitempty"`
		UserID       string `json:"user_id,omitempty"`
		IsBot        bool   `json:"is_bot,omitempty"`
	} `json:"authorizations,omitempty"`
	EventContext string `json:"event_context,omitempty"`
	EventID      string `json:"event_id,omitempty"`
	EventTime    int    `json:"event_time,omitempty"`
	Challenge    string `json:"challenge,omitempty"`
}

type File struct {
	ID                 string `json:"id"`
	Created            int    `json:"created"`
	Timestamp          int    `json:"timestamp"`
	Name               string `json:"name"`
	Title              string `json:"title"`
	Mimetype           string `json:"mimetype"`
	Filetype           string `json:"filetype"`
	PrettyType         string `json:"pretty_type"`
	User               string `json:"user"`
	Editable           bool   `json:"editable"`
	Size               int    `json:"size"`
	Mode               string `json:"mode"`
	IsExternal         bool   `json:"is_external"`
	ExternalType       string `json:"external_type"`
	IsPublic           bool   `json:"is_public"`
	PublicURLShared    bool   `json:"public_url_shared"`
	DisplayAsBot       bool   `json:"display_as_bot"`
	Username           string `json:"username"`
	URLPrivate         string `json:"url_private"`
	URLPrivateDownload string `json:"url_private_download"`
	MediaDisplayType   string `json:"media_display_type"`
	Thumb64            string `json:"thumb_64"`
	Thumb80            string `json:"thumb_80"`
	Thumb360           string `json:"thumb_360"`
	Thumb360W          int    `json:"thumb_360_w"`
	Thumb360H          int    `json:"thumb_360_h"`
	Thumb160           string `json:"thumb_160"`
	OriginalW          int    `json:"original_w"`
	OriginalH          int    `json:"original_h"`
	ThumbTiny          string `json:"thumb_tiny"`
	Permalink          string `json:"permalink"`
	PermalinkPublic    string `json:"permalink_public"`
	HasRichPreview     bool   `json:"has_rich_preview"`
}

type fileResponse struct {
	OK    bool   `json:"ok"`
	File  File   `json:"file"`
	Error string `json:"error"`
}

type UserInfo struct {
	Ok   bool `json:"ok"`
	User struct {
		ID       string `json:"id"`
		TeamID   string `json:"team_id"`
		Name     string `json:"name"`
		Deleted  bool   `json:"deleted"`
		Color    string `json:"color"`
		RealName string `json:"real_name"`
		Tz       string `json:"tz"`
		TzLabel  string `json:"tz_label"`
		TzOffset int    `json:"tz_offset"`
		Profile  struct {
			AvatarHash            string `json:"avatar_hash"`
			StatusText            string `json:"status_text"`
			StatusEmoji           string `json:"status_emoji"`
			RealName              string `json:"real_name"`
			DisplayName           string `json:"display_name"`
			RealNameNormalized    string `json:"real_name_normalized"`
			DisplayNameNormalized string `json:"display_name_normalized"`
			Email                 string `json:"email"`
			ImageOriginal         string `json:"image_original"`
			Image24               string `json:"image_24"`
			Image32               string `json:"image_32"`
			Image48               string `json:"image_48"`
			Image72               string `json:"image_72"`
			Image192              string `json:"image_192"`
			Image512              string `json:"image_512"`
			Team                  string `json:"team"`
		} `json:"profile"`
	} `json:"user"`
}

type FileParams struct {
	File     []byte `json:"file,omitempty"`
	FileName string `json:"filename,omitempty"`
	Channels string `json:"channels,omitempty"`
}
