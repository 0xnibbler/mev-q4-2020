package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/pkg/errors"
)

const (
	token  = ""
	chatID = 0
)

type telegramSendMsg struct {
	ChatID             int    `json:"chat_id"`
	Text               string `json:"text"`
	ParseMode          string `json:"parse_mode"`
	Silent             bool   `json:"silent"`
	DisableLinkPreview bool   `json:"disable_web_page_preview"`
}

var client = &http.Client{Timeout: 10 * time.Second}

func Message(ti time.Time, message string, notify bool) error {
	return sendMessage(fmt.Sprintf(`%s
%s`,
		ti.Format("Mon, 02 Jan 15:04:05"),
		message), notify)
}

func sendMessage(text string, notify bool) error {
	url := "https://api.telegram.org/bot" + token + "/sendMessage"
	body, err := json.Marshal(&telegramSendMsg{
		Text:               text,
		ChatID:             chatID,
		ParseMode:          "HTML",
		Silent:             !notify,
		DisableLinkPreview: true,
	})

	if err != nil {
		return err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
	if err != nil {
		return err
	}

	req.Header.Add("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}

	if resp.StatusCode != http.StatusOK {
		return errors.New("bad status")
	}

	return nil
}
