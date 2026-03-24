package wechat

import (
	"bytes"
	"context"
	"io"
	"strconv"
	"strings"
	"time"
)

// ItemType identifies the kind of content in a message item.
type ItemType int

const (
	ItemText  ItemType = 1
	ItemImage ItemType = 2
	ItemVoice ItemType = 3
	ItemFile  ItemType = 4
	ItemVideo ItemType = 5
)

// String returns a stable log-friendly item type name.
func (t ItemType) String() string {
	switch t {
	case ItemText:
		return "text"
	case ItemImage:
		return "image"
	case ItemVoice:
		return "voice"
	case ItemFile:
		return "file"
	case ItemVideo:
		return "video"
	default:
		return "unknown"
	}
}

// Message represents an inbound WeChat message with one or more items.
type Message struct {
	ID         string
	FromUserID string
	Timestamp  time.Time
	Items      []Item
}

// Text concatenates all TextItem content. Returns "" if no text items.
func (m Message) Text() string {
	var sb strings.Builder
	for _, item := range m.Items {
		if item.Type == ItemText && item.Text != nil {
			sb.WriteString(item.Text.Content)
		}
	}
	return sb.String()
}

// Item is a single content element in a message.
type Item struct {
	Type  ItemType
	Text  *TextItem
	Image *ImageItem
	Voice *VoiceItem
	File  *FileItem
	Video *VideoItem
}

// TextItem contains text content.
type TextItem struct {
	Content string
}

// ImageItem contains image metadata and download capability.
type ImageItem struct {
	Width    int
	Height   int
	download func() (io.ReadCloser, error)
}

// Download fetches and decrypts the image from CDN.
func (i *ImageItem) Download() (io.ReadCloser, error) {
	return i.download()
}

// VoiceItem contains voice message metadata and download capability.
type VoiceItem struct {
	Duration   int    // playtime in milliseconds
	EncodeType int    // codec: 1=pcm, 2=adpcm, 6=silk, 7=mp3, etc.
	Text       string // voice-to-text transcription (may be empty)
	download   func() (io.ReadCloser, error)
}

// Download fetches and decrypts the voice message from CDN.
func (i *VoiceItem) Download() (io.ReadCloser, error) {
	return i.download()
}

// FileItem contains file metadata and download capability.
type FileItem struct {
	FileName string
	FileSize int64
	download func() (io.ReadCloser, error)
}

// Download fetches and decrypts the file from CDN.
func (i *FileItem) Download() (io.ReadCloser, error) {
	return i.download()
}

// VideoItem contains video metadata and download capability.
type VideoItem struct {
	Duration int // play_length in seconds
	Width    int
	Height   int
	download func() (io.ReadCloser, error)
}

// Download fetches and decrypts the video from CDN.
func (i *VideoItem) Download() (io.ReadCloser, error) {
	return i.download()
}

func (c *ilinkClient) newDownloadFunc(encryptQueryParam, aesKeyBase64 string) func() (io.ReadCloser, error) {
	return func() (io.ReadCloser, error) {
		data, err := c.downloadFromCDN(context.Background(), encryptQueryParam, aesKeyBase64)
		if err != nil {
			return nil, err
		}
		return io.NopCloser(bytes.NewReader(data)), nil
	}
}

func (c *ilinkClient) parseMessage(raw wireMessage) Message {
	msg := Message{
		ID:         strconv.Itoa(raw.MessageID),
		FromUserID: raw.FromUserID,
		Timestamp:  time.UnixMilli(raw.CreateTimeMs),
	}

	for _, wi := range raw.ItemList {
		item := Item{Type: ItemType(wi.Type)}
		switch ItemType(wi.Type) {
		case ItemText:
			if wi.TextItem != nil {
				item.Text = &TextItem{Content: wi.TextItem.Text}
			}
		case ItemImage:
			if wi.ImageItem != nil {
				img := &ImageItem{
					Width:  wi.ImageItem.ThumbWidth,
					Height: wi.ImageItem.ThumbHeight,
				}
				if wi.ImageItem.Media != nil && wi.ImageItem.Media.EncryptQueryParam != "" {
					img.download = c.newDownloadFunc(
						wi.ImageItem.Media.EncryptQueryParam,
						wi.ImageItem.Media.AESKey,
					)
				}
				item.Image = img
			}
		case ItemVoice:
			if wi.VoiceItem != nil {
				v := &VoiceItem{
					Duration:   wi.VoiceItem.Playtime,
					EncodeType: wi.VoiceItem.EncodeType,
					Text:       wi.VoiceItem.Text,
				}
				if wi.VoiceItem.Media != nil && wi.VoiceItem.Media.EncryptQueryParam != "" {
					v.download = c.newDownloadFunc(
						wi.VoiceItem.Media.EncryptQueryParam,
						wi.VoiceItem.Media.AESKey,
					)
				}
				item.Voice = v
			}
		case ItemFile:
			if wi.FileItem != nil {
				size, _ := strconv.ParseInt(wi.FileItem.Len, 10, 64)
				f := &FileItem{
					FileName: wi.FileItem.FileName,
					FileSize: size,
				}
				if wi.FileItem.Media != nil && wi.FileItem.Media.EncryptQueryParam != "" {
					f.download = c.newDownloadFunc(
						wi.FileItem.Media.EncryptQueryParam,
						wi.FileItem.Media.AESKey,
					)
				}
				item.File = f
			}
		case ItemVideo:
			if wi.VideoItem != nil {
				v := &VideoItem{
					Duration: wi.VideoItem.PlayLength,
					Width:    wi.VideoItem.ThumbWidth,
					Height:   wi.VideoItem.ThumbHeight,
				}
				if wi.VideoItem.Media != nil && wi.VideoItem.Media.EncryptQueryParam != "" {
					v.download = c.newDownloadFunc(
						wi.VideoItem.Media.EncryptQueryParam,
						wi.VideoItem.Media.AESKey,
					)
				}
				item.Video = v
			}
		default:
			continue
		}
		msg.Items = append(msg.Items, item)
	}
	return msg
}
