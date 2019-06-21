package freshchat

/*
 * Handler for FreshChat
 */
import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	//"github.com/go-errors/errors"
	"github.com/nyaruka/courier"
	"github.com/nyaruka/courier/handlers"
	"github.com/nyaruka/courier/utils"
	"github.com/nyaruka/gocommon/urns"
)

var (
	apiURL          = "https://api.freshchat.com/v2"
	signatureHeader = "X-FreshChat-Signature"
)

func init() {
	courier.RegisterHandler(newHandler("FC", "FreshChat", true))
}

type handler struct {
	handlers.BaseHandler
	validateSignatures bool
}

func newHandler(channelType courier.ChannelType, name string, validateSignatures bool) courier.ChannelHandler {
	return &handler{handlers.NewBaseHandler(courier.ChannelType("FC"), "FreshChat"), validateSignatures}
}

// Initialize is called by the engine once everything is loaded
func (h *handler) Initialize(s courier.Server) error {
	h.SetServer(s)
	s.AddHandlerRoute(h, http.MethodPost, "receive", h.receiveMessage)
	return nil
}
func (h *handler) receiveMessage(ctx context.Context, channel courier.Channel, w http.ResponseWriter, r *http.Request) ([]courier.Event, error) {
	err := h.validateSignature(channel, r)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}
	payload := &moPayload{}
	err = handlers.DecodeAndValidateJSON(payload, r)
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}

	// no message? ignore this
	if payload.Data.Message.ActorID == "" {
		return nil, handlers.WriteAndLogRequestIgnored(ctx, h, channel, w, r, "Ignoring request, no message")
	}

	// something we sent? ignore this
	if payload.Data.Message.ActorType == "agent" {
		return nil, handlers.WriteAndLogRequestIgnored(ctx, h, channel, w, r, "Ignoring request, Agent Message")
	}

	// create our date from the timestamp
	date := payload.Data.Message.CreatedTime

	// create our URN
	urn := urns.NilURN
	var urnparts strings.Builder
	urnparts.WriteString(payload.Data.Message.ChannelID)
	urnparts.WriteString("/")
	urnparts.WriteString(payload.Data.Message.ActorID)
	urn, err = urns.NewURNFromParts(channel.Schemes()[0], urnparts.String(), "", "")
	if err != nil {
		return nil, handlers.WriteAndLogRequestError(ctx, h, channel, w, r, err)
	}
	text := ""
	mediaURL := ""
	// our text is either "text" or "image"
	for _, data := range payload.Data.Message.MessageParts {
		if data.Text != nil {
			text = data.Text.Content
		}
		if data.Image != nil {
			mediaURL = string(data.Image.URL)
		}
	}
	// build our msg
	msg := h.Backend().NewIncomingMsg(channel, urn, text).WithReceivedOn(date)

	//add image
	if mediaURL != "" {
		msg.WithAttachment(mediaURL)
	}
	// and finally write our message
	return handlers.WriteMsgsAndResponse(ctx, h, []courier.Msg{msg}, w, r)
}

func (h *handler) SendMsg(ctx context.Context, msg courier.Msg) (courier.MsgStatus, error) {

	agentID := msg.Channel().StringConfigForKey(courier.ConfigUsername, "")
	if agentID == "" {
		return nil, fmt.Errorf("missing 'agent_id' config for FC channel")
	}

	authToken := msg.Channel().StringConfigForKey(courier.ConfigAuthToken, "")
	if authToken == "" {
		return nil, fmt.Errorf("missing 'auth_token' config for FC channel")
	}

	user := strings.Split(fmt.Sprintf("%v", msg.URN().Path()), "/")
	status := h.Backend().NewMsgStatusForID(msg.Channel(), msg.ID(), courier.MsgErrored)
	url := apiURL + "/conversations"

	// create base payload
	payload := &messagePayload{
		Messages: []Messages{
			{
				MessageParts: []MessageParts{},
				ActorID:      agentID,
				ActorType:    "agent",
			}},
		ChannelID: user[0],
		Users: []Users{
			{
				ID: user[1],
			},
		},
	}
	// build message payload

	if len(msg.Text()) > 0 {
		text := msg.Text()
		var msgtext = new(MessageParts)
		msgtext.Text = &Text{Content: text}
		payload.Messages[0].MessageParts = append(payload.Messages[0].MessageParts, *msgtext)
	}

	if len(msg.Attachments()) > 0 {
		mediaURL := msg.Attachments()[0]
		var msgimage = new(MessageParts)
		msgimage.Image = &Image{URL: mediaURL}
		payload.Messages[0].MessageParts = append(payload.Messages[0].MessageParts, *msgimage)
	}

	jsonBody, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	var bearer = "Bearer " + authToken
	req.Header.Set("Authorization", bearer)

	rr, err := utils.MakeHTTPRequest(req)

	// record our status and log
	log := courier.NewChannelLogFromRR("Message Sent", msg.Channel(), msg.ID(), rr).WithError("Message Send Error", err)
	status.AddLog(log)
	if err != nil {
		return status, err
	}
	status.SetStatus(courier.MsgWired)

	return status, nil
}

func (h *handler) validateSignature(c courier.Channel, r *http.Request) error {
	if !h.validateSignatures {
		return nil
	}
	var rsaPubKey = []byte(c.StringConfigForKey(courier.ConfigPassword, ""))

	actual := r.Header.Get(signatureHeader)
	if actual == "" {
		return fmt.Errorf("missing request signature")
	}
	buf, _ := ioutil.ReadAll(r.Body)
	rdr1 := ioutil.NopCloser(bytes.NewBuffer(buf))
	rdr2 := ioutil.NopCloser(bytes.NewBuffer(buf))
	token, err := ioutil.ReadAll(rdr1)
	if err != nil {
		return fmt.Errorf("unable to read Body, %s", err.Error())
	}
	r.Body = rdr2

	var b64Sig = []byte(actual)
	block, _ := pem.Decode(rsaPubKey)
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		panic("failed to parse DER encoded public key: " + err.Error())
	}
	hash := sha256.New()
	if _, err := bytes.NewReader(token).WriteTo(hash); err != nil {
		return fmt.Errorf("unable to hash signed token, %s", err.Error())
	}
	decodedSig, err := base64.StdEncoding.DecodeString(string(b64Sig))
	if err != nil {
		return fmt.Errorf("unable to decode base64 signature, %s", err.Error())
	}

	if err := rsa.VerifyPKCS1v15(pub.(*rsa.PublicKey), crypto.SHA256, hash.Sum(nil), decodedSig); err != nil {
		return fmt.Errorf("unable to verify signature, %s", err.Error())
	}

	return nil
}

type messagePayload struct {
	Messages  []Messages `json:"messages"`
	Status    string     `json:"status,omitempty"`
	ChannelID string     `json:"channel_id"`
	Users     []Users    `json:"users"`
}
type Messages struct {
	MessageParts []MessageParts `json:"message_parts"`
	ActorID      string         `json:"actor_id"`
	ActorType    string         `json:"actor_type"`
}

type Users struct {
	ID string `json:"id"`
}
type moPayload struct {
	Actor      Actor     `json:"actor"`
	Action     string    `json:"action"`
	ActionTime time.Time `json:"action_time"`
	Data       Data      `json:"data"`
}
type Actor struct {
	ActorType string `json:"actor_type"`
	ActorID   string `json:"actor_id"`
}
type Text struct {
	Content string `json:"content,omitempty"`
}
type MessageParts struct {
	Text  *Text  `json:"text,omitempty"`
	Image *Image `json:"image,omitempty"`
}
type Message struct {
	MessageParts   []MessageParts `json:"message_parts"`
	AppID          string         `json:"app_id"`
	ActorID        string         `json:"actor_id"`
	ID             string         `json:"id"`
	ChannelID      string         `json:"channel_id"`
	ConversationID string         `json:"conversation_id"`
	MessageType    string         `json:"message_type"`
	ActorType      string         `json:"actor_type"`
	CreatedTime    time.Time      `json:"created_time"`
}
type Data struct {
	Message *Message `json:"message,omitempty"`
}
type Image struct {
	URL string `json:"url,omitempty"`
}
