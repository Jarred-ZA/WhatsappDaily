package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

// getEnv returns the value of an environment variable or a default value
func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

var (
	whisperModelPath = getEnv("WHISPER_MODEL_PATH", "/Users/jarred.duplessisplatform45.com/whisper-models/ggml-base.en.bin")
	whisperBinPath   = getEnv("WHISPER_BIN_PATH", "/opt/homebrew/bin/whisper-cli")
	ffmpegBinPath    = getEnv("FFMPEG_BIN_PATH", "/opt/homebrew/bin/ffmpeg")
	storeDir         = getEnv("STORE_DIR", "store")
	bridgeAPIKey     = os.Getenv("BRIDGE_API_KEY")
)

// Auth state for web-based QR pairing
var (
	authMu      sync.RWMutex
	currentQR   string // current QR code string (empty if none/authenticated)
	authStatus  string = "initializing"
	pairCode    string // pair code for phone-based pairing
)

func setAuthState(status, qr, pair string) {
	authMu.Lock()
	defer authMu.Unlock()
	authStatus = status
	currentQR = qr
	pairCode = pair
}

func getAuthState() (string, string, string) {
	authMu.RLock()
	defer authMu.RUnlock()
	return authStatus, currentQR, pairCode
}

const authPageHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>WhatsApp Bridge</title>
<style>
  * { margin: 0; padding: 0; box-sizing: border-box; }
  body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; background: #0a0a0a; color: #e0e0e0; min-height: 100vh; display: flex; align-items: center; justify-content: center; }
  .container { text-align: center; padding: 2rem; max-width: 500px; }
  h1 { font-size: 1.5rem; margin-bottom: 0.5rem; color: #25D366; }
  .subtitle { color: #888; margin-bottom: 2rem; font-size: 0.9rem; }
  .status { padding: 1rem; border-radius: 12px; margin-bottom: 1.5rem; font-weight: 500; }
  .status.connected { background: rgba(37, 211, 102, 0.15); color: #25D366; border: 1px solid rgba(37, 211, 102, 0.3); }
  .status.waiting { background: rgba(255, 193, 7, 0.15); color: #FFC107; border: 1px solid rgba(255, 193, 7, 0.3); }
  .status.error { background: rgba(244, 67, 54, 0.15); color: #F44336; border: 1px solid rgba(244, 67, 54, 0.3); }
  .status.init { background: rgba(100, 100, 100, 0.15); color: #aaa; border: 1px solid rgba(100, 100, 100, 0.3); }
  #qr-container { background: #fff; padding: 1rem; border-radius: 12px; display: inline-block; margin-bottom: 1.5rem; }
  #qr-container canvas { display: block; }
  .pair-code { font-size: 2rem; letter-spacing: 0.5rem; font-weight: 700; color: #25D366; margin: 1rem 0; font-family: monospace; }
  .instructions { color: #888; font-size: 0.85rem; line-height: 1.6; margin-bottom: 1.5rem; }
  .btn { background: #25D366; color: #000; border: none; padding: 0.75rem 1.5rem; border-radius: 8px; font-size: 0.9rem; font-weight: 600; cursor: pointer; transition: opacity 0.2s; }
  .btn:hover { opacity: 0.85; }
  .btn.danger { background: #F44336; color: #fff; }
  .btn:disabled { opacity: 0.4; cursor: not-allowed; }
  .hidden { display: none; }
  .spinner { display: inline-block; width: 20px; height: 20px; border: 2px solid rgba(255,255,255,0.3); border-top-color: #25D366; border-radius: 50%; animation: spin 0.8s linear infinite; margin-right: 0.5rem; vertical-align: middle; }
  @keyframes spin { to { transform: rotate(360deg); } }
</style>
<script src="https://cdn.jsdelivr.net/npm/qrcode/build/qrcode.min.js"></script>
</head>
<body>
<div class="container">
  <h1>WhatsApp Bridge</h1>
  <p class="subtitle">Daily Summary Service</p>

  <div id="status" class="status init"><span class="spinner"></span> Initializing...</div>

  <div id="qr-section" class="hidden">
    <div id="qr-container"><canvas id="qr-canvas"></canvas></div>
    <p class="instructions">Open WhatsApp on your phone<br>Go to Settings > Linked Devices > Link a Device<br>Scan this QR code</p>
  </div>

  <div id="pair-section" class="hidden">
    <div class="pair-code" id="pair-code-display"></div>
    <p class="instructions">Open WhatsApp on your phone<br>Go to Settings > Linked Devices > Link a Device<br>Enter this code instead of scanning</p>
  </div>

  <div id="connected-section" class="hidden">
    <p class="instructions" style="margin-bottom: 1rem;">WhatsApp is connected and receiving messages.</p>
    <button class="btn danger" onclick="reauth()">Disconnect & Re-authenticate</button>
  </div>

  <div id="reauth-section" class="hidden">
    <button class="btn" onclick="startAuth()">Generate New QR Code</button>
  </div>
</div>

<script>
let lastQR = '';
let pollInterval;

async function poll() {
  try {
    const res = await fetch('/api/auth/status');
    const data = await res.json();
    updateUI(data);
  } catch(e) {
    document.getElementById('status').className = 'status error';
    document.getElementById('status').innerHTML = 'Connection error';
  }
}

function updateUI(data) {
  const statusEl = document.getElementById('status');
  const qrSection = document.getElementById('qr-section');
  const pairSection = document.getElementById('pair-section');
  const connSection = document.getElementById('connected-section');
  const reauthSection = document.getElementById('reauth-section');

  qrSection.classList.add('hidden');
  pairSection.classList.add('hidden');
  connSection.classList.add('hidden');
  reauthSection.classList.add('hidden');

  if (data.status === 'connected') {
    statusEl.className = 'status connected';
    statusEl.innerHTML = 'Connected';
    connSection.classList.remove('hidden');
  } else if (data.status === 'waiting_for_qr') {
    statusEl.className = 'status waiting';
    statusEl.innerHTML = '<span class="spinner"></span> Waiting for QR scan...';
    if (data.qr_code && data.qr_code !== lastQR) {
      lastQR = data.qr_code;
      QRCode.toCanvas(document.getElementById('qr-canvas'), data.qr_code, { width: 280, margin: 1 });
    }
    qrSection.classList.remove('hidden');
  } else if (data.status === 'waiting_for_pair') {
    statusEl.className = 'status waiting';
    statusEl.innerHTML = '<span class="spinner"></span> Waiting for pair code entry...';
    document.getElementById('pair-code-display').textContent = data.pair_code || '';
    pairSection.classList.remove('hidden');
  } else if (data.status === 'logged_out' || data.status === 'disconnected') {
    statusEl.className = 'status error';
    statusEl.innerHTML = 'Disconnected';
    reauthSection.classList.remove('hidden');
  } else {
    statusEl.className = 'status init';
    statusEl.innerHTML = '<span class="spinner"></span> ' + (data.status || 'Initializing...');
  }
}

async function reauth() {
  if (!confirm('This will disconnect WhatsApp. Continue?')) return;
  await fetch('/api/auth/logout', { method: 'POST' });
  poll();
}

async function startAuth() {
  await fetch('/api/auth/start', { method: 'POST' });
  poll();
}

pollInterval = setInterval(poll, 2000);
poll();
</script>
</body>
</html>`

// checkAPIKey validates the X-API-Key header. Returns true if valid.
// If BRIDGE_API_KEY is empty (local dev), all requests are allowed.
func checkAPIKey(w http.ResponseWriter, r *http.Request) bool {
	if bridgeAPIKey == "" {
		return true
	}
	key := r.Header.Get("X-API-Key")
	if key != bridgeAPIKey {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

// Message represents a chat message for our client
type Message struct {
	Time      time.Time
	Sender    string
	Content   string
	IsFromMe  bool
	MediaType string
	Filename  string
}

// Database handler for storing message history
type MessageStore struct {
	db *sql.DB
}

// Initialize message store
func NewMessageStore() (*MessageStore, error) {
	// Create directory for database if it doesn't exist
	if err := os.MkdirAll(storeDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create store directory: %v", err)
	}

	// Open SQLite database for messages
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s/messages.db?_foreign_keys=on", storeDir))
	if err != nil {
		return nil, fmt.Errorf("failed to open message database: %v", err)
	}

	// Create tables if they don't exist
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS chats (
			jid TEXT PRIMARY KEY,
			name TEXT,
			last_message_time TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS messages (
			id TEXT,
			chat_jid TEXT,
			sender TEXT,
			content TEXT,
			timestamp TIMESTAMP,
			is_from_me BOOLEAN,
			media_type TEXT,
			filename TEXT,
			url TEXT,
			media_key BLOB,
			file_sha256 BLOB,
			file_enc_sha256 BLOB,
			file_length INTEGER,
			transcription TEXT,
			PRIMARY KEY (id, chat_jid),
			FOREIGN KEY (chat_jid) REFERENCES chats(jid)
		);
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create tables: %v", err)
	}

	// Migration: add transcription column if it doesn't exist (for existing databases)
	_, err = db.Exec(`ALTER TABLE messages ADD COLUMN transcription TEXT`)
	if err != nil {
		// Ignore error if column already exists
		if !strings.Contains(err.Error(), "duplicate column") {
			// Not a duplicate column error, but still not fatal
		}
		err = nil
	}
	return &MessageStore{db: db}, nil
}

// Close the database connection
func (store *MessageStore) Close() error {
	return store.db.Close()
}

// Store a chat in the database
func (store *MessageStore) StoreChat(jid, name string, lastMessageTime time.Time) error {
	_, err := store.db.Exec(
		"INSERT OR REPLACE INTO chats (jid, name, last_message_time) VALUES (?, ?, ?)",
		jid, name, lastMessageTime,
	)
	return err
}

// Store a message in the database
func (store *MessageStore) StoreMessage(id, chatJID, sender, content string, timestamp time.Time, isFromMe bool,
	mediaType, filename, url string, mediaKey, fileSHA256, fileEncSHA256 []byte, fileLength uint64) error {
	// Only store if there's actual content or media
	if content == "" && mediaType == "" {
		return nil
	}

	_, err := store.db.Exec(
		`INSERT OR REPLACE INTO messages
		(id, chat_jid, sender, content, timestamp, is_from_me, media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, chatJID, sender, content, timestamp, isFromMe, mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength,
	)
	return err
}

// Get messages from a chat
func (store *MessageStore) GetMessages(chatJID string, limit int) ([]Message, error) {
	rows, err := store.db.Query(
		"SELECT sender, content, timestamp, is_from_me, media_type, filename FROM messages WHERE chat_jid = ? ORDER BY timestamp DESC LIMIT ?",
		chatJID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []Message
	for rows.Next() {
		var msg Message
		var timestamp time.Time
		err := rows.Scan(&msg.Sender, &msg.Content, &timestamp, &msg.IsFromMe, &msg.MediaType, &msg.Filename)
		if err != nil {
			return nil, err
		}
		msg.Time = timestamp
		messages = append(messages, msg)
	}

	return messages, nil
}

// Get all chats
func (store *MessageStore) GetChats() (map[string]time.Time, error) {
	rows, err := store.db.Query("SELECT jid, last_message_time FROM chats ORDER BY last_message_time DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	chats := make(map[string]time.Time)
	for rows.Next() {
		var jid string
		var lastMessageTime time.Time
		err := rows.Scan(&jid, &lastMessageTime)
		if err != nil {
			return nil, err
		}
		chats[jid] = lastMessageTime
	}

	return chats, nil
}

// StoreTranscription updates the transcription for a message in the database
func (store *MessageStore) StoreTranscription(id, chatJID, transcription string) error {
	_, err := store.db.Exec(
		"UPDATE messages SET transcription = ? WHERE id = ? AND chat_jid = ?",
		transcription, id, chatJID,
	)
	return err
}

// RecentMessage represents a message returned by the /api/messages/recent endpoint
type RecentMessage struct {
	ID            string `json:"id"`
	ChatJID       string `json:"chat_jid"`
	ChatName      string `json:"chat_name"`
	Sender        string `json:"sender"`
	Content       string `json:"content"`
	Timestamp     string `json:"timestamp"`
	IsFromMe      bool   `json:"is_from_me"`
	MediaType     string `json:"media_type,omitempty"`
	Transcription string `json:"transcription,omitempty"`
}

// GetRecentMessages returns messages from the last N hours with chat names
func (store *MessageStore) GetRecentMessages(hours int) ([]RecentMessage, error) {
	cutoff := time.Now().UTC().Add(-time.Duration(hours) * time.Hour)

	rows, err := store.db.Query(`
		SELECT m.id, m.chat_jid, COALESCE(c.name, m.chat_jid) as chat_name,
		       m.sender, COALESCE(m.content, '') as content, m.timestamp, m.is_from_me,
		       COALESCE(m.media_type, '') as media_type, COALESCE(m.transcription, '') as transcription
		FROM messages m
		LEFT JOIN chats c ON m.chat_jid = c.jid
		WHERE m.timestamp >= ?
		ORDER BY m.timestamp ASC
	`, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var messages []RecentMessage
	for rows.Next() {
		var msg RecentMessage
		var ts time.Time
		err := rows.Scan(&msg.ID, &msg.ChatJID, &msg.ChatName, &msg.Sender, &msg.Content,
			&ts, &msg.IsFromMe, &msg.MediaType, &msg.Transcription)
		if err != nil {
			return nil, err
		}
		msg.Timestamp = ts.Format(time.RFC3339)
		messages = append(messages, msg)
	}

	return messages, nil
}

// transcribeAudio converts audio to WAV then transcribes using whisper-cli
func transcribeAudio(audioPath string) (string, error) {
	// Check whisper binary exists
	if _, err := os.Stat(whisperBinPath); os.IsNotExist(err) {
		return "", fmt.Errorf("whisper-cli not found at %s, install with: brew install whisper-cpp", whisperBinPath)
	}

	// Check model exists
	if _, err := os.Stat(whisperModelPath); os.IsNotExist(err) {
		return "", fmt.Errorf("whisper model not found at %s", whisperModelPath)
	}

	// Convert to 16kHz mono WAV (whisper-cli requires wav input)
	wavPath := strings.TrimSuffix(audioPath, filepath.Ext(audioPath)) + "_transcribe.wav"
	defer os.Remove(wavPath)

	convertCmd := exec.Command(ffmpegBinPath,
		"-i", audioPath,
		"-ar", "16000",
		"-ac", "1",
		"-c:a", "pcm_s16le",
		"-y",
		wavPath,
	)
	convertOut, err := convertCmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("ffmpeg conversion failed: %v, output: %s", err, string(convertOut))
	}

	// Run whisper-cli
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	whisperCmd := exec.CommandContext(ctx, whisperBinPath,
		"-m", whisperModelPath,
		"-f", wavPath,
		"--no-timestamps",
	)

	// Capture stdout separately from stderr
	var stdout, stderr bytes.Buffer
	whisperCmd.Stdout = &stdout
	whisperCmd.Stderr = &stderr

	err = whisperCmd.Run()
	if err != nil {
		return "", fmt.Errorf("whisper-cli failed: %v, stderr: %s", err, stderr.String())
	}

	transcription := strings.TrimSpace(stdout.String())
	return transcription, nil
}

// Extract text content from a message
func extractTextContent(msg *waProto.Message) string {
	if msg == nil {
		return ""
	}

	// Try to get text content
	if text := msg.GetConversation(); text != "" {
		return text
	} else if extendedText := msg.GetExtendedTextMessage(); extendedText != nil {
		return extendedText.GetText()
	}

	return ""
}

// SendMessageResponse represents the response for the send message API
type SendMessageResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// SendMessageRequest represents the request body for the send message API
type SendMessageRequest struct {
	Recipient string `json:"recipient"`
	Message   string `json:"message"`
	MediaPath string `json:"media_path,omitempty"`
}

// Function to send a WhatsApp message
func sendWhatsAppMessage(client *whatsmeow.Client, recipient string, message string, mediaPath string) (bool, string) {
	if !client.IsConnected() {
		return false, "Not connected to WhatsApp"
	}

	// Create JID for recipient
	var recipientJID types.JID
	var err error

	// Check if recipient is a JID
	isJID := strings.Contains(recipient, "@")

	if isJID {
		// Parse the JID string
		recipientJID, err = types.ParseJID(recipient)
		if err != nil {
			return false, fmt.Sprintf("Error parsing JID: %v", err)
		}
	} else {
		// Create JID from phone number
		recipientJID = types.JID{
			User:   recipient,
			Server: "s.whatsapp.net",
		}
	}

	msg := &waProto.Message{}

	// Check if we have media to send
	if mediaPath != "" {
		// Read media file
		mediaData, err := os.ReadFile(mediaPath)
		if err != nil {
			return false, fmt.Sprintf("Error reading media file: %v", err)
		}

		// Determine media type and mime type based on file extension
		fileExt := strings.ToLower(mediaPath[strings.LastIndex(mediaPath, ".")+1:])
		var mediaType whatsmeow.MediaType
		var mimeType string

		switch fileExt {
		case "jpg", "jpeg":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/jpeg"
		case "png":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/png"
		case "gif":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/gif"
		case "webp":
			mediaType = whatsmeow.MediaImage
			mimeType = "image/webp"
		case "ogg":
			mediaType = whatsmeow.MediaAudio
			mimeType = "audio/ogg; codecs=opus"
		case "mp4":
			mediaType = whatsmeow.MediaVideo
			mimeType = "video/mp4"
		case "avi":
			mediaType = whatsmeow.MediaVideo
			mimeType = "video/avi"
		case "mov":
			mediaType = whatsmeow.MediaVideo
			mimeType = "video/quicktime"
		default:
			mediaType = whatsmeow.MediaDocument
			mimeType = "application/octet-stream"
		}

		// Upload media to WhatsApp servers
		resp, err := client.Upload(context.Background(), mediaData, mediaType)
		if err != nil {
			return false, fmt.Sprintf("Error uploading media: %v", err)
		}

		fmt.Println("Media uploaded", resp)

		switch mediaType {
		case whatsmeow.MediaImage:
			msg.ImageMessage = &waProto.ImageMessage{
				Caption:       proto.String(message),
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
			}
		case whatsmeow.MediaAudio:
			var seconds uint32 = 30
			var waveform []byte = nil

			if strings.Contains(mimeType, "ogg") {
				analyzedSeconds, analyzedWaveform, err := analyzeOggOpus(mediaData)
				if err == nil {
					seconds = analyzedSeconds
					waveform = analyzedWaveform
				} else {
					return false, fmt.Sprintf("Failed to analyze Ogg Opus file: %v", err)
				}
			}

			msg.AudioMessage = &waProto.AudioMessage{
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
				Seconds:       proto.Uint32(seconds),
				PTT:           proto.Bool(true),
				Waveform:      waveform,
			}
		case whatsmeow.MediaVideo:
			msg.VideoMessage = &waProto.VideoMessage{
				Caption:       proto.String(message),
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
			}
		case whatsmeow.MediaDocument:
			msg.DocumentMessage = &waProto.DocumentMessage{
				Title:         proto.String(mediaPath[strings.LastIndex(mediaPath, "/")+1:]),
				Caption:       proto.String(message),
				Mimetype:      proto.String(mimeType),
				URL:           &resp.URL,
				DirectPath:    &resp.DirectPath,
				MediaKey:      resp.MediaKey,
				FileEncSHA256: resp.FileEncSHA256,
				FileSHA256:    resp.FileSHA256,
				FileLength:    &resp.FileLength,
			}
		}
	} else {
		msg.Conversation = proto.String(message)
	}

	// Send message
	_, err = client.SendMessage(context.Background(), recipientJID, msg)

	if err != nil {
		return false, fmt.Sprintf("Error sending message: %v", err)
	}

	return true, fmt.Sprintf("Message sent to %s", recipient)
}

// Extract media info from a message
func extractMediaInfo(msg *waProto.Message) (mediaType string, filename string, url string, mediaKey []byte, fileSHA256 []byte, fileEncSHA256 []byte, fileLength uint64) {
	if msg == nil {
		return "", "", "", nil, nil, nil, 0
	}

	if img := msg.GetImageMessage(); img != nil {
		return "image", "image_" + time.Now().Format("20060102_150405") + ".jpg",
			img.GetURL(), img.GetMediaKey(), img.GetFileSHA256(), img.GetFileEncSHA256(), img.GetFileLength()
	}

	if vid := msg.GetVideoMessage(); vid != nil {
		return "video", "video_" + time.Now().Format("20060102_150405") + ".mp4",
			vid.GetURL(), vid.GetMediaKey(), vid.GetFileSHA256(), vid.GetFileEncSHA256(), vid.GetFileLength()
	}

	if aud := msg.GetAudioMessage(); aud != nil {
		return "audio", "audio_" + time.Now().Format("20060102_150405") + ".ogg",
			aud.GetURL(), aud.GetMediaKey(), aud.GetFileSHA256(), aud.GetFileEncSHA256(), aud.GetFileLength()
	}

	if doc := msg.GetDocumentMessage(); doc != nil {
		filename := doc.GetFileName()
		if filename == "" {
			filename = "document_" + time.Now().Format("20060102_150405")
		}
		return "document", filename,
			doc.GetURL(), doc.GetMediaKey(), doc.GetFileSHA256(), doc.GetFileEncSHA256(), doc.GetFileLength()
	}

	return "", "", "", nil, nil, nil, 0
}

// Handle regular incoming messages with media support
func handleMessage(client *whatsmeow.Client, messageStore *MessageStore, msg *events.Message, logger waLog.Logger) {
	chatJID := msg.Info.Chat.String()
	sender := msg.Info.Sender.User

	name := GetChatName(client, messageStore, msg.Info.Chat, chatJID, nil, sender, logger)

	err := messageStore.StoreChat(chatJID, name, msg.Info.Timestamp)
	if err != nil {
		logger.Warnf("Failed to store chat: %v", err)
	}

	content := extractTextContent(msg.Message)
	mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength := extractMediaInfo(msg.Message)

	if content == "" && mediaType == "" {
		return
	}

	err = messageStore.StoreMessage(
		msg.Info.ID, chatJID, sender, content, msg.Info.Timestamp, msg.Info.IsFromMe,
		mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength,
	)

	if err != nil {
		logger.Warnf("Failed to store message: %v", err)
	} else {
		timestamp := msg.Info.Timestamp.Format("2006-01-02 15:04:05")
		direction := "←"
		if msg.Info.IsFromMe {
			direction = "→"
		}

		if mediaType != "" {
			fmt.Printf("[%s] %s %s: [%s: %s] %s\n", timestamp, direction, sender, mediaType, filename, content)
		} else if content != "" {
			fmt.Printf("[%s] %s %s: %s\n", timestamp, direction, sender, content)
		}

		// Auto-transcribe audio messages in background
		if mediaType == "audio" {
			go func(msgID, cJID string) {
				logger.Infof("Auto-transcribing voice note %s in %s...", msgID, cJID)
				success, _, _, audioPath, dlErr := downloadMedia(client, messageStore, msgID, cJID)
				if !success || dlErr != nil {
					logger.Warnf("Failed to download audio for transcription: %v", dlErr)
					return
				}
				transcription, tErr := transcribeAudio(audioPath)
				if tErr != nil {
					logger.Warnf("Failed to transcribe audio: %v", tErr)
					return
				}
				if transcription != "" {
					if sErr := messageStore.StoreTranscription(msgID, cJID, transcription); sErr != nil {
						logger.Warnf("Failed to store transcription: %v", sErr)
					} else {
						logger.Infof("Transcribed voice note %s: %s", msgID, transcription)
					}
				}
			}(msg.Info.ID, chatJID)
		}
	}
}

// DownloadMediaRequest represents the request body for the download media API
type DownloadMediaRequest struct {
	MessageID string `json:"message_id"`
	ChatJID   string `json:"chat_jid"`
}

// DownloadMediaResponse represents the response for the download media API
type DownloadMediaResponse struct {
	Success  bool   `json:"success"`
	Message  string `json:"message"`
	Filename string `json:"filename,omitempty"`
	Path     string `json:"path,omitempty"`
}

// Store additional media info in the database
func (store *MessageStore) StoreMediaInfo(id, chatJID, url string, mediaKey, fileSHA256, fileEncSHA256 []byte, fileLength uint64) error {
	_, err := store.db.Exec(
		"UPDATE messages SET url = ?, media_key = ?, file_sha256 = ?, file_enc_sha256 = ?, file_length = ? WHERE id = ? AND chat_jid = ?",
		url, mediaKey, fileSHA256, fileEncSHA256, fileLength, id, chatJID,
	)
	return err
}

// Get media info from the database
func (store *MessageStore) GetMediaInfo(id, chatJID string) (string, string, string, []byte, []byte, []byte, uint64, error) {
	var mediaType, filename, url string
	var mediaKey, fileSHA256, fileEncSHA256 []byte
	var fileLength uint64

	err := store.db.QueryRow(
		"SELECT media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length FROM messages WHERE id = ? AND chat_jid = ?",
		id, chatJID,
	).Scan(&mediaType, &filename, &url, &mediaKey, &fileSHA256, &fileEncSHA256, &fileLength)

	return mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength, err
}

// MediaDownloader implements the whatsmeow.DownloadableMessage interface
type MediaDownloader struct {
	URL           string
	DirectPath    string
	MediaKey      []byte
	FileLength    uint64
	FileSHA256    []byte
	FileEncSHA256 []byte
	MediaType     whatsmeow.MediaType
}

func (d *MediaDownloader) GetDirectPath() string    { return d.DirectPath }
func (d *MediaDownloader) GetURL() string            { return d.URL }
func (d *MediaDownloader) GetMediaKey() []byte       { return d.MediaKey }
func (d *MediaDownloader) GetFileLength() uint64     { return d.FileLength }
func (d *MediaDownloader) GetFileSHA256() []byte     { return d.FileSHA256 }
func (d *MediaDownloader) GetFileEncSHA256() []byte  { return d.FileEncSHA256 }
func (d *MediaDownloader) GetMediaType() whatsmeow.MediaType { return d.MediaType }

// Function to download media from a message
func downloadMedia(client *whatsmeow.Client, messageStore *MessageStore, messageID, chatJID string) (bool, string, string, string, error) {
	var mediaType, filename, url string
	var mediaKey, fileSHA256, fileEncSHA256 []byte
	var fileLength uint64
	var err error

	chatDir := fmt.Sprintf("%s/%s", storeDir, strings.ReplaceAll(chatJID, ":", "_"))
	localPath := ""

	mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength, err = messageStore.GetMediaInfo(messageID, chatJID)

	if err != nil {
		err = messageStore.db.QueryRow(
			"SELECT media_type, filename FROM messages WHERE id = ? AND chat_jid = ?",
			messageID, chatJID,
		).Scan(&mediaType, &filename)

		if err != nil {
			return false, "", "", "", fmt.Errorf("failed to find message: %v", err)
		}
	}

	if mediaType == "" {
		return false, "", "", "", fmt.Errorf("not a media message")
	}

	if err := os.MkdirAll(chatDir, 0755); err != nil {
		return false, "", "", "", fmt.Errorf("failed to create chat directory: %v", err)
	}

	localPath = fmt.Sprintf("%s/%s", chatDir, filename)

	absPath, err := filepath.Abs(localPath)
	if err != nil {
		return false, "", "", "", fmt.Errorf("failed to get absolute path: %v", err)
	}

	if _, err := os.Stat(localPath); err == nil {
		return true, mediaType, filename, absPath, nil
	}

	if url == "" || len(mediaKey) == 0 || len(fileSHA256) == 0 || len(fileEncSHA256) == 0 || fileLength == 0 {
		return false, "", "", "", fmt.Errorf("incomplete media information for download")
	}

	fmt.Printf("Attempting to download media for message %s in chat %s...\n", messageID, chatJID)

	directPath := extractDirectPathFromURL(url)

	var waMediaType whatsmeow.MediaType
	switch mediaType {
	case "image":
		waMediaType = whatsmeow.MediaImage
	case "video":
		waMediaType = whatsmeow.MediaVideo
	case "audio":
		waMediaType = whatsmeow.MediaAudio
	case "document":
		waMediaType = whatsmeow.MediaDocument
	default:
		return false, "", "", "", fmt.Errorf("unsupported media type: %s", mediaType)
	}

	downloader := &MediaDownloader{
		URL:           url,
		DirectPath:    directPath,
		MediaKey:      mediaKey,
		FileLength:    fileLength,
		FileSHA256:    fileSHA256,
		FileEncSHA256: fileEncSHA256,
		MediaType:     waMediaType,
	}

	mediaData, err := client.Download(context.Background(), downloader)
	if err != nil {
		return false, "", "", "", fmt.Errorf("failed to download media: %v", err)
	}

	if err := os.WriteFile(localPath, mediaData, 0644); err != nil {
		return false, "", "", "", fmt.Errorf("failed to save media file: %v", err)
	}

	fmt.Printf("Successfully downloaded %s media to %s (%d bytes)\n", mediaType, absPath, len(mediaData))
	return true, mediaType, filename, absPath, nil
}

// Extract direct path from a WhatsApp media URL
func extractDirectPathFromURL(url string) string {
	parts := strings.SplitN(url, ".net/", 2)
	if len(parts) < 2 {
		return url
	}

	pathPart := parts[1]
	pathPart = strings.SplitN(pathPart, "?", 2)[0]
	return "/" + pathPart
}

// isPhoneNumber checks if a string looks like a phone number (all digits, 10-15 chars)
func isPhoneNumber(s string) bool {
	if len(s) < 8 || len(s) > 15 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// resolveContactName tries to get a contact's display name from the WhatsApp client
func resolveContactName(client *whatsmeow.Client, jidStr string) string {
	if client == nil || client.Store == nil {
		return ""
	}
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return ""
	}

	// For groups, try group info
	if jid.Server == "g.us" {
		groupInfo, err := client.GetGroupInfo(context.Background(), jid)
		if err == nil && groupInfo.Name != "" {
			return groupInfo.Name
		}
		return ""
	}

	// For contacts, try the contact store
	contact, err := client.Store.Contacts.GetContact(context.Background(), jid)
	if err == nil {
		if contact.FullName != "" {
			return contact.FullName
		}
		if contact.PushName != "" {
			return contact.PushName
		}
		if contact.BusinessName != "" {
			return contact.BusinessName
		}
	}
	return ""
}

// Start a REST API server to expose the WhatsApp client functionality
func startRESTServer(client *whatsmeow.Client, container *sqlstore.Container, messageStore *MessageStore, port int, logger waLog.Logger) {
	// Serve web UI at root
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, authPageHTML)
	})

	// Auth status endpoint (no API key - used by web UI)
	http.HandleFunc("/api/auth/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status, qr, pair := getAuthState()
		// Override with live connection status
		if client.IsConnected() && client.Store.ID != nil {
			status = "connected"
		}
		resp := map[string]string{"status": status}
		if qr != "" {
			resp["qr_code"] = qr
		}
		if pair != "" {
			resp["pair_code"] = pair
		}
		json.NewEncoder(w).Encode(resp)
	})

	// Start new auth flow
	http.HandleFunc("/api/auth/start", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		go startQRAuth(client, container, messageStore, logger)
		json.NewEncoder(w).Encode(map[string]string{"status": "starting"})
	})

	// Logout endpoint
	http.HandleFunc("/api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		client.Disconnect()
		if client.Store.ID != nil {
			client.Logout(context.Background())
		}
		setAuthState("logged_out", "", "")
		json.NewEncoder(w).Encode(map[string]string{"status": "logged_out"})
	})

	// Health check endpoint (no auth)
	http.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		status := "disconnected"
		if client.IsConnected() {
			status = "connected"
		}
		json.NewEncoder(w).Encode(map[string]string{"status": status})
	})

	// Recent messages endpoint (auth required)
	http.HandleFunc("/api/messages/recent", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !checkAPIKey(w, r) {
			return
		}

		hoursStr := r.URL.Query().Get("hours")
		hours := 48
		if hoursStr != "" {
			if h, err := strconv.Atoi(hoursStr); err == nil && h > 0 {
				hours = h
			}
		}

		messages, err := messageStore.GetRecentMessages(hours)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		// Resolve contact names for chat names and senders that are phone numbers
		nameCache := make(map[string]string)
		for i, msg := range messages {
			// Resolve chat name if it looks like a phone number
			if isPhoneNumber(msg.ChatName) {
				if resolved, ok := nameCache[msg.ChatName]; ok {
					messages[i].ChatName = resolved
				} else if name := resolveContactName(client, msg.ChatJID); name != "" {
					nameCache[msg.ChatName] = name
					messages[i].ChatName = name
					// Also update the chat in DB for future queries
					messageStore.StoreChat(msg.ChatJID, name, time.Now())
				}
			}
			// Resolve sender if it looks like a phone number
			if isPhoneNumber(msg.Sender) && !msg.IsFromMe {
				senderJID := msg.Sender + "@s.whatsapp.net"
				if resolved, ok := nameCache[msg.Sender]; ok {
					messages[i].Sender = resolved
				} else if name := resolveContactName(client, senderJID); name != "" {
					nameCache[msg.Sender] = name
					messages[i].Sender = name
				}
			}
			if msg.IsFromMe {
				messages[i].Sender = "Jarred"
			}
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(messages)
	})

	// Handler for sending messages (auth required)
	http.HandleFunc("/api/send", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !checkAPIKey(w, r) {
			return
		}

		var req SendMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request format", http.StatusBadRequest)
			return
		}

		if req.Recipient == "" {
			http.Error(w, "Recipient is required", http.StatusBadRequest)
			return
		}

		if req.Message == "" && req.MediaPath == "" {
			http.Error(w, "Message or media path is required", http.StatusBadRequest)
			return
		}

		fmt.Println("Received request to send message", req.Message, req.MediaPath)

		success, message := sendWhatsAppMessage(client, req.Recipient, req.Message, req.MediaPath)
		fmt.Println("Message sent", success, message)

		w.Header().Set("Content-Type", "application/json")
		if !success {
			w.WriteHeader(http.StatusInternalServerError)
		}

		json.NewEncoder(w).Encode(SendMessageResponse{
			Success: success,
			Message: message,
		})
	})

	// Handler for downloading media (auth required)
	http.HandleFunc("/api/download", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !checkAPIKey(w, r) {
			return
		}

		var req DownloadMediaRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request format", http.StatusBadRequest)
			return
		}

		if req.MessageID == "" || req.ChatJID == "" {
			http.Error(w, "Message ID and Chat JID are required", http.StatusBadRequest)
			return
		}

		success, mediaType, filename, path, err := downloadMedia(client, messageStore, req.MessageID, req.ChatJID)

		w.Header().Set("Content-Type", "application/json")

		if !success || err != nil {
			errMsg := "Unknown error"
			if err != nil {
				errMsg = err.Error()
			}

			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(DownloadMediaResponse{
				Success: false,
				Message: fmt.Sprintf("Failed to download media: %s", errMsg),
			})
			return
		}

		json.NewEncoder(w).Encode(DownloadMediaResponse{
			Success:  true,
			Message:  fmt.Sprintf("Successfully downloaded %s media", mediaType),
			Filename: filename,
			Path:     path,
		})
	})

	// Handler for transcribing audio messages (auth required)
	http.HandleFunc("/api/transcribe", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !checkAPIKey(w, r) {
			return
		}

		var req DownloadMediaRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request format", http.StatusBadRequest)
			return
		}

		if req.MessageID == "" || req.ChatJID == "" {
			http.Error(w, "Message ID and Chat JID are required", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")

		// Check if we already have a transcription
		var existingTranscription sql.NullString
		messageStore.db.QueryRow(
			"SELECT transcription FROM messages WHERE id = ? AND chat_jid = ?",
			req.MessageID, req.ChatJID,
		).Scan(&existingTranscription)

		if existingTranscription.Valid && existingTranscription.String != "" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success":       true,
				"message":       "Transcription already exists",
				"transcription": existingTranscription.String,
			})
			return
		}

		success, mediaType, _, audioPath, err := downloadMedia(client, messageStore, req.MessageID, req.ChatJID)
		if !success || err != nil {
			errMsg := "Unknown error"
			if err != nil {
				errMsg = err.Error()
			}
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": fmt.Sprintf("Failed to download media: %s", errMsg),
			})
			return
		}

		if mediaType != "audio" {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": "Message is not an audio message",
			})
			return
		}

		transcription, err := transcribeAudio(audioPath)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"success": false,
				"message": fmt.Sprintf("Transcription failed: %v", err),
			})
			return
		}

		if storeErr := messageStore.StoreTranscription(req.MessageID, req.ChatJID, transcription); storeErr != nil {
			fmt.Printf("Warning: failed to store transcription: %v\n", storeErr)
		}

		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":       true,
			"message":       "Transcription completed",
			"transcription": transcription,
		})
	})

	// Start the server
	serverAddr := fmt.Sprintf(":%d", port)
	fmt.Printf("Starting REST API server on %s...\n", serverAddr)

	go func() {
		if err := http.ListenAndServe(serverAddr, nil); err != nil {
			fmt.Printf("REST API server error: %v\n", err)
		}
	}()
}

// startQRAuth initiates QR-based authentication via the web UI
func startQRAuth(client *whatsmeow.Client, container *sqlstore.Container, messageStore *MessageStore, logger waLog.Logger) {
	// Get a fresh device store
	deviceStore := container.NewDevice()
	client.Store = deviceStore

	setAuthState("waiting_for_qr", "", "")

	qrChan, _ := client.GetQRChannel(context.Background())
	err := client.Connect()
	if err != nil {
		logger.Errorf("Failed to connect for QR auth: %v", err)
		setAuthState("error", "", "")
		return
	}

	for evt := range qrChan {
		if evt.Event == "code" {
			setAuthState("waiting_for_qr", evt.Code, "")
			logger.Infof("New QR code generated, waiting for scan...")
		} else if evt.Event == "success" {
			setAuthState("connected", "", "")
			logger.Infof("QR auth successful!")
			return
		} else if evt.Event == "timeout" {
			setAuthState("logged_out", "", "")
			logger.Warnf("QR auth timed out")
			return
		}
	}
}

func main() {
	logger := waLog.Stdout("Client", "INFO", true)
	logger.Infof("Starting WhatsApp bridge...")

	dbLog := waLog.Stdout("Database", "INFO", true)

	if err := os.MkdirAll(storeDir, 0755); err != nil {
		logger.Errorf("Failed to create store directory: %v", err)
		return
	}

	container, err := sqlstore.New(context.Background(), "sqlite3", fmt.Sprintf("file:%s/whatsapp.db?_foreign_keys=on", storeDir), dbLog)
	if err != nil {
		logger.Errorf("Failed to connect to database: %v", err)
		return
	}

	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		if err == sql.ErrNoRows {
			deviceStore = container.NewDevice()
			logger.Infof("Created new device")
		} else {
			logger.Errorf("Failed to get device: %v", err)
			return
		}
	}

	client := whatsmeow.NewClient(deviceStore, logger)
	if client == nil {
		logger.Errorf("Failed to create WhatsApp client")
		return
	}

	messageStore, err := NewMessageStore()
	if err != nil {
		logger.Errorf("Failed to initialize message store: %v", err)
		return
	}
	defer messageStore.Close()

	client.AddEventHandler(func(evt interface{}) {
		switch v := evt.(type) {
		case *events.Message:
			handleMessage(client, messageStore, v, logger)
		case *events.HistorySync:
			handleHistorySync(client, messageStore, v, logger)
		case *events.Connected:
			logger.Infof("Connected to WhatsApp")
			setAuthState("connected", "", "")
		case *events.LoggedOut:
			logger.Warnf("Device logged out")
			setAuthState("logged_out", "", "")
		}
	})

	// Determine port
	port := 8080
	if p := os.Getenv("PORT"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil {
			port = parsed
		}
	}

	// Start HTTP server FIRST so web UI is available during auth
	startRESTServer(client, container, messageStore, port, logger)
	fmt.Printf("REST server is running on port %d\n", port)

	// Now handle authentication
	if client.Store.ID == nil {
		// No existing session - need to authenticate
		pairPhone := os.Getenv("PAIR_PHONE")
		if pairPhone != "" {
			// Use pair code
			setAuthState("waiting_for_pair", "", "")
			err = client.Connect()
			if err != nil {
				logger.Errorf("Failed to connect: %v", err)
				setAuthState("error", "", "")
			} else {
				code, err := client.PairPhone(context.Background(), pairPhone, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
				if err != nil {
					logger.Errorf("Failed to get pair code: %v", err)
					setAuthState("error", "", "")
				} else {
					setAuthState("waiting_for_pair", "", code)
					fmt.Printf("\nPAIR CODE: %s\n", code)
					fmt.Printf("Enter this code on your phone, or visit the web UI.\n\n")

					// Wait for pairing
					for i := 0; i < 60; i++ {
						if client.Store.ID != nil {
							setAuthState("connected", "", "")
							break
						}
						time.Sleep(5 * time.Second)
					}
				}
			}
		} else {
			// Use QR code via web UI
			fmt.Println("\nNo existing session. Open the web UI to scan QR code.")
			go startQRAuth(client, container, messageStore, logger)
		}
	} else {
		// Existing session - just connect
		setAuthState("connecting", "", "")
		err = client.Connect()
		if err != nil {
			logger.Errorf("Failed to connect: %v", err)
			setAuthState("error", "", "")
		} else {
			setAuthState("connected", "", "")
			fmt.Println("Connected to WhatsApp!")
		}
	}

	exitChan := make(chan os.Signal, 1)
	signal.Notify(exitChan, syscall.SIGINT, syscall.SIGTERM)

	<-exitChan

	fmt.Println("Disconnecting...")
	client.Disconnect()
}

// GetChatName determines the appropriate name for a chat based on JID and other info
func GetChatName(client *whatsmeow.Client, messageStore *MessageStore, jid types.JID, chatJID string, conversation interface{}, sender string, logger waLog.Logger) string {
	var existingName string
	err := messageStore.db.QueryRow("SELECT name FROM chats WHERE jid = ?", chatJID).Scan(&existingName)
	if err == nil && existingName != "" {
		return existingName
	}

	var name string

	if jid.Server == "g.us" {
		if conversation != nil {
			var displayName, convName *string
			v := reflect.ValueOf(conversation)
			if v.Kind() == reflect.Ptr && !v.IsNil() {
				v = v.Elem()

				if displayNameField := v.FieldByName("DisplayName"); displayNameField.IsValid() && displayNameField.Kind() == reflect.Ptr && !displayNameField.IsNil() {
					dn := displayNameField.Elem().String()
					displayName = &dn
				}

				if nameField := v.FieldByName("Name"); nameField.IsValid() && nameField.Kind() == reflect.Ptr && !nameField.IsNil() {
					n := nameField.Elem().String()
					convName = &n
				}
			}

			if displayName != nil && *displayName != "" {
				name = *displayName
			} else if convName != nil && *convName != "" {
				name = *convName
			}
		}

		if name == "" {
			groupInfo, err := client.GetGroupInfo(context.Background(), jid)
			if err == nil && groupInfo.Name != "" {
				name = groupInfo.Name
			} else {
				name = fmt.Sprintf("Group %s", jid.User)
			}
		}
	} else {
		contact, err := client.Store.Contacts.GetContact(context.Background(), jid)
		if err == nil && contact.FullName != "" {
			name = contact.FullName
		} else if sender != "" {
			name = sender
		} else {
			name = jid.User
		}
	}

	return name
}

// Handle history sync events
func handleHistorySync(client *whatsmeow.Client, messageStore *MessageStore, historySync *events.HistorySync, logger waLog.Logger) {
	fmt.Printf("Received history sync event with %d conversations\n", len(historySync.Data.Conversations))

	syncedCount := 0
	for _, conversation := range historySync.Data.Conversations {
		if conversation.ID == nil {
			continue
		}

		chatJID := *conversation.ID

		jid, err := types.ParseJID(chatJID)
		if err != nil {
			logger.Warnf("Failed to parse JID %s: %v", chatJID, err)
			continue
		}

		name := GetChatName(client, messageStore, jid, chatJID, conversation, "", logger)

		messages := conversation.Messages
		if len(messages) > 0 {
			latestMsg := messages[0]
			if latestMsg == nil || latestMsg.Message == nil {
				continue
			}

			timestamp := time.Time{}
			if ts := latestMsg.Message.GetMessageTimestamp(); ts != 0 {
				timestamp = time.Unix(int64(ts), 0)
			} else {
				continue
			}

			messageStore.StoreChat(chatJID, name, timestamp)

			for _, msg := range messages {
				if msg == nil || msg.Message == nil {
					continue
				}

				var content string
				if msg.Message.Message != nil {
					if conv := msg.Message.Message.GetConversation(); conv != "" {
						content = conv
					} else if ext := msg.Message.Message.GetExtendedTextMessage(); ext != nil {
						content = ext.GetText()
					}
				}

				var mediaType, filename, url string
				var mediaKey, fileSHA256, fileEncSHA256 []byte
				var fileLength uint64

				if msg.Message.Message != nil {
					mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength = extractMediaInfo(msg.Message.Message)
				}

				if content == "" && mediaType == "" {
					continue
				}

				var sender string
				isFromMe := false
				if msg.Message.Key != nil {
					if msg.Message.Key.FromMe != nil {
						isFromMe = *msg.Message.Key.FromMe
					}
					if !isFromMe && msg.Message.Key.Participant != nil && *msg.Message.Key.Participant != "" {
						sender = *msg.Message.Key.Participant
					} else if isFromMe {
						sender = client.Store.ID.User
					} else {
						sender = jid.User
					}
				} else {
					sender = jid.User
				}

				msgID := ""
				if msg.Message.Key != nil && msg.Message.Key.ID != nil {
					msgID = *msg.Message.Key.ID
				}

				timestamp := time.Time{}
				if ts := msg.Message.GetMessageTimestamp(); ts != 0 {
					timestamp = time.Unix(int64(ts), 0)
				} else {
					continue
				}

				err = messageStore.StoreMessage(
					msgID, chatJID, sender, content, timestamp, isFromMe,
					mediaType, filename, url, mediaKey, fileSHA256, fileEncSHA256, fileLength,
				)
				if err != nil {
					logger.Warnf("Failed to store history message: %v", err)
				} else {
					syncedCount++
				}
			}
		}
	}

	fmt.Printf("History sync complete. Stored %d messages.\n", syncedCount)
}

// Request history sync from the server
func requestHistorySync(client *whatsmeow.Client) {
	if client == nil || !client.IsConnected() || client.Store.ID == nil {
		fmt.Println("Client is not ready. Cannot request history sync.")
		return
	}

	historyMsg := client.BuildHistorySyncRequest(nil, 100)
	if historyMsg == nil {
		fmt.Println("Failed to build history sync request.")
		return
	}

	_, err := client.SendMessage(context.Background(), types.JID{
		Server: "s.whatsapp.net",
		User:   "status",
	}, historyMsg)

	if err != nil {
		fmt.Printf("Failed to request history sync: %v\n", err)
	} else {
		fmt.Println("History sync requested. Waiting for server response...")
	}
}

// analyzeOggOpus tries to extract duration and generate a simple waveform from an Ogg Opus file
func analyzeOggOpus(data []byte) (duration uint32, waveform []byte, err error) {
	if len(data) < 4 || string(data[0:4]) != "OggS" {
		return 0, nil, fmt.Errorf("not a valid Ogg file (missing OggS signature)")
	}

	var lastGranule uint64
	var sampleRate uint32 = 48000
	var preSkip uint16 = 0
	var foundOpusHead bool

	for i := 0; i < len(data); {
		if i+27 >= len(data) {
			break
		}

		if string(data[i:i+4]) != "OggS" {
			i++
			continue
		}

		granulePos := binary.LittleEndian.Uint64(data[i+6 : i+14])
		pageSeqNum := binary.LittleEndian.Uint32(data[i+18 : i+22])
		numSegments := int(data[i+26])

		if i+27+numSegments >= len(data) {
			break
		}
		segmentTable := data[i+27 : i+27+numSegments]

		pageSize := 27 + numSegments
		for _, segLen := range segmentTable {
			pageSize += int(segLen)
		}

		if !foundOpusHead && pageSeqNum <= 1 {
			pageData := data[i : i+pageSize]
			headPos := bytes.Index(pageData, []byte("OpusHead"))
			if headPos >= 0 && headPos+12 < len(pageData) {
				headPos += 8
				if headPos+12 <= len(pageData) {
					preSkip = binary.LittleEndian.Uint16(pageData[headPos+10 : headPos+12])
					sampleRate = binary.LittleEndian.Uint32(pageData[headPos+12 : headPos+16])
					foundOpusHead = true
				}
			}
		}

		if granulePos != 0 {
			lastGranule = granulePos
		}

		i += pageSize
	}

	if lastGranule > 0 {
		durationSeconds := float64(lastGranule-uint64(preSkip)) / float64(sampleRate)
		duration = uint32(math.Ceil(durationSeconds))
	} else {
		durationEstimate := float64(len(data)) / 2000.0
		duration = uint32(durationEstimate)
	}

	if duration < 1 {
		duration = 1
	} else if duration > 300 {
		duration = 300
	}

	waveform = placeholderWaveform(duration)
	return duration, waveform, nil
}

func min(x, y int) int {
	if x < y {
		return x
	}
	return y
}

func placeholderWaveform(duration uint32) []byte {
	const waveformLength = 64
	waveform := make([]byte, waveformLength)

	rand.Seed(int64(duration))

	baseAmplitude := 35.0
	frequencyFactor := float64(min(int(duration), 120)) / 30.0

	for i := range waveform {
		pos := float64(i) / float64(waveformLength)
		val := baseAmplitude * math.Sin(pos*math.Pi*frequencyFactor*8)
		val += (baseAmplitude / 2) * math.Sin(pos*math.Pi*frequencyFactor*16)
		val += (rand.Float64() - 0.5) * 15
		fadeInOut := math.Sin(pos * math.Pi)
		val = val * (0.7 + 0.3*fadeInOut)
		val = val + 50

		if val < 0 {
			val = 0
		} else if val > 100 {
			val = 100
		}

		waveform[i] = byte(val)
	}

	return waveform
}
