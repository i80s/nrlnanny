package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

//go:embed control.html play.html live.html live_mult.html login.html i18n.js
var webAssets embed.FS

type AudioFile struct {
	Name      string `json:"name"`
	Timestamp string `json:"timestamp"`
	URL       string `json:"url"`
}

// 从文件名提取时间
func parseTimeFromFilename(filename string) (time.Time, error) {
	base := strings.TrimSuffix(filename, ".wav")
	parts := strings.Split(base, "_")
	if len(parts) < 3 {
		return time.Time{}, fmt.Errorf("格式错误: %s", filename)
	}
	datePart := parts[0]
	timePart := parts[1]
	dtStr := datePart + " " + timePart[:2] + ":" + timePart[2:4] + ":" + timePart[4:6]
	return time.Parse("2006-01-02 15:04:05", dtStr)
}

func play() {
	http.HandleFunc("/", serveIndex)          // Live homepage
	http.HandleFunc("/control", serveControl) // Authenticated dashboard
	http.HandleFunc("/login", serveLogin)
	http.HandleFunc("/logout", serveLogout)
	http.HandleFunc("/play", servePlay)              // Public recordings browser
	http.HandleFunc("/live", serveLive)              // Live broadcast page
	http.HandleFunc("/live-mult", serveLiveMult)     // Multi-room live monitor page
	http.HandleFunc("/ws/live", handleLiveWS)        // Live WebSocket (same port, reverse-proxy/mobile friendly)
	http.HandleFunc("/pcm-worklet.js", serveWorklet) // AudioWorklet JS for Safari
	http.HandleFunc("/i18n.js", serveI18n)
	http.HandleFunc("/dirs", listDirs)       // 获取所有日期目录
	http.HandleFunc("/dir/", listFilesInDir) // 获取某目录下文件
	http.Handle("/recordings/", http.StripPrefix("/recordings/", http.FileServer(http.Dir(conf.System.RecoderFilePath))))

	// Web API
	http.HandleFunc("/api/status", apiStatus)
	http.HandleFunc("/api/music", controlPageOnly(apiMusic))
	http.HandleFunc("/api/radio", controlPageOnly(apiRadio))
	http.HandleFunc("/api/control", controlPageOnly(apiControl))
	http.HandleFunc("/api/live-config", apiLiveConfig)
	http.HandleFunc("/api/live-mult-config", apiLiveMultConfig)
	http.HandleFunc("/api/ping", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("pong"))
	})

	log.Printf("服务器启动中：http://0.0.0.0:%s\n", conf.System.WebPort)
	server := &http.Server{
		Addr: ":" + conf.System.WebPort,
	}
	log.Fatal(server.ListenAndServe())
}

func serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	serveLive(w, r)
}

func serveControl(w http.ResponseWriter, r *http.Request) {
	if !conf.System.EnableControlPage {
		http.NotFound(w, r)
		return
	}
	if !controlAuthenticated(r) {
		http.Redirect(w, r, "/login?next=/control", http.StatusSeeOther)
		return
	}
	content, err := webAssets.ReadFile("control.html")
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Write(content)
}

func controlPageOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !conf.System.EnableControlPage {
			http.NotFound(w, r)
			return
		}
		if !controlAuthenticated(r) {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func controlPageHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		controlPageOnly(next.ServeHTTP)(w, r)
	})
}

func serveWorklet(w http.ResponseWriter, r *http.Request) {
	const js = `class PCMPlayerProcessor extends AudioWorkletProcessor {
    constructor() {
        super();
        this.buffer = new Float32Array(0);
        this.port.onmessage = (e) => {
            const incoming = e.data;
            const newBuf = new Float32Array(this.buffer.length + incoming.length);
            newBuf.set(this.buffer);
            newBuf.set(incoming, this.buffer.length);
            this.buffer = newBuf;
            if (this.buffer.length > 16000) {
                this.buffer = this.buffer.slice(this.buffer.length - 16000);
            }
        };
    }
    process(inputs, outputs) {
        const output = outputs[0][0];
        if (!output) return true;
        if (this.buffer.length >= output.length) {
            output.set(this.buffer.subarray(0, output.length));
            this.buffer = this.buffer.slice(output.length);
        } else {
            output.fill(0);
            if (this.buffer.length > 0) {
                output.set(this.buffer);
                this.buffer = new Float32Array(0);
            }
        }
        return true;
    }
}
registerProcessor('pcm-player', PCMPlayerProcessor);`
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write([]byte(js))
}

func serveI18n(w http.ResponseWriter, r *http.Request) {
	content, err := webAssets.ReadFile("i18n.js")
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Write(content)
}

func serveLive(w http.ResponseWriter, r *http.Request) {
	content, err := webAssets.ReadFile("live.html")
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Write(content)
}

func serveLiveMult(w http.ResponseWriter, r *http.Request) {
	content, err := webAssets.ReadFile("live_mult.html")
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Write(content)
}

func servePlay(w http.ResponseWriter, r *http.Request) {
	content, err := webAssets.ReadFile("play.html")
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(content)
}

func apiStatus(w http.ResponseWriter, r *http.Request) {
	displayMu.Lock()
	s := statusState
	c := cronState
	p := progressState

	displayMu.Unlock()

	data := map[string]any{
		"callsign":        conf.System.Callsign,
		"ssid":            conf.System.SSID,
		"server":          conf.System.Server,
		"port":            conf.System.Port,
		"control_enabled": conf.System.EnableControlPage,
		"authenticated":   controlAuthenticated(r),
		"volume":          int(conf.System.Volume * 100),
		"status":          s,
		"cron":            c,
		"progress":        p,
		"playing":         conf.System.MusicPlaying,
		"duck_scale":      int(conf.System.DuckScale * 100),
		"duck_mic_pcm":    conf.System.DuckMicPCM,
		"duck_music_pcm":  conf.System.DuckMusicPCM,
		"record_mic":      isRecordMicEnabled(),
		"record_voice":    isRecordingEnabled(),
		"send_opus":       isSendOpusEnabled(),
		"cron_enabled":    isCronEnabled(),
		"time_enabled":    isTimeEnabled(),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func apiMusic(w http.ResponseWriter, r *http.Request) {
	musicstateMu.Lock()
	files := currentQueue.files
	playingID := currentPlayingID
	musicstateMu.Unlock()

	data := map[string]any{
		"files":     files,
		"playingID": playingID,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func apiRadio(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeRadioState(w)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Action string `json:"action"`
		ID     string `json:"id"`
		Name   string `json:"name"`
		URL    string `json:"url"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	var err error
	switch req.Action {
	case "add":
		_, err = saveRadioStation("", req.Name, req.URL)
	case "update":
		_, err = saveRadioStation(req.ID, req.Name, req.URL)
		if err == nil {
			_, activeID, playing, _ := radioSnapshot()
			if playing && activeID == req.ID {
				err = startRadio(req.ID)
			}
		}
	case "delete":
		err = deleteRadioStation(req.ID)
	case "play":
		err = startRadio(req.ID)
	case "stop":
		stopRadio()
	default:
		err = fmt.Errorf("unsupported radio action")
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Action == "add" || req.Action == "update" || req.Action == "delete" {
		saveConfig()
	}
	writeRadioState(w)
}

func writeRadioState(w http.ResponseWriter) {
	stations, activeID, playing, status := radioSnapshot()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"stations":  stations,
		"active_id": activeID,
		"playing":   playing,
		"status":    status,
	})
}

func apiControl(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Action string  `json:"action"`
		Value  float64 `json:"value"`
		ID     int     `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	switch req.Action {
	case "play_id":
		switchToLocalMusic()
		PlayMusicByID(req.ID)
	case "pause":
		if isRadioPlaying() {
			stopRadio()
			break
		}
		select {
		case pausemusic <- true:
		default:
		}
		conf.System.MusicPlaying = !conf.System.MusicPlaying
		saveConfig()
	case "next":
		switchToLocalMusic()
		select {
		case nextmusic <- true:
		default:
		}
	case "prev":
		switchToLocalMusic()
		select {
		case lastmusic <- true:
		default:
		}
	case "volume":
		if req.Value >= 0 && req.Value <= 2 {
			conf.System.Volume = req.Value
			updateVolumeDisplay()
			saveConfig()
		}
	case "duck_scale":
		if req.Value >= 0 && req.Value <= 1 {
			conf.System.DuckScale = req.Value
			log.Printf("Duck Scale updated to: %.2f", req.Value)
			saveConfig()
		}
	case "duck_mic_pcm":
		conf.System.DuckMicPCM = !conf.System.DuckMicPCM
		log.Printf("Duck Mic PCM updated to: %v", conf.System.DuckMicPCM)
		saveConfig()
	case "duck_music_pcm":
		conf.System.DuckMusicPCM = !conf.System.DuckMusicPCM
		log.Printf("Duck Music PCM updated to: %v", conf.System.DuckMusicPCM)
		saveConfig()
	case "record_mic":
		conf.System.RecordMic = !conf.System.RecordMic
		setRecordMicEnabled(conf.System.RecordMic)
		log.Printf("Mic Capture updated to: %v", conf.System.RecordMic)
		saveConfig()
	case "record_voice":
		conf.System.RecordVoice = !conf.System.RecordVoice
		setRecordingEnabled(conf.System.RecordVoice)
		if !conf.System.RecordVoice {
			recorder.Stop()
		}
		log.Printf("Voice Recording updated to: %v", conf.System.RecordVoice)
		saveConfig()
	case "send_opus":
		conf.System.SendOpus = !conf.System.SendOpus
		setSendOpusEnabled(conf.System.SendOpus)
		log.Printf("Voice codec updated to: %s", map[bool]string{true: "Opus 16 kHz", false: "G.711 8 kHz"}[conf.System.SendOpus])
		saveConfig()
	case "music_toggle":
		conf.System.MusicPlaying = !conf.System.MusicPlaying
		select {
		case pausemusic <- true:
		default:
		}
		log.Printf("Music playing updated to: %v", conf.System.MusicPlaying)
		saveConfig()
	case "cron_toggle":
		conf.System.EnableCron = !conf.System.EnableCron
		setCronEnabled(conf.System.EnableCron)
		if !conf.System.EnableCron {
			updateCronInfo("Cron Disabled")
		}
		log.Printf("Cron enabled updated to: %v", conf.System.EnableCron)
		saveConfig()
	case "time_toggle":
		conf.System.EnableTimePlay = !conf.System.EnableTimePlay
		setTimeEnabled(conf.System.EnableTimePlay)
		log.Printf("Time play enabled updated to: %v", conf.System.EnableTimePlay)
		saveConfig()
	}

	w.WriteHeader(http.StatusOK)
}

func apiLiveConfig(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(conf.System.LiveTitle)
	subtitle := strings.TrimSpace(conf.System.LiveSubtitle)

	if title == "" {
		title = "BROADCAST TOPIC"
	}
	if subtitle == "" {
		subtitle = "Live Broadcast"
	}

	data := map[string]string{
		"title":    title,
		"subtitle": subtitle,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func apiLiveMultConfig(w http.ResponseWriter, r *http.Request) {
	server := strings.TrimSpace(conf.System.Server)
	server = strings.TrimRight(server, "/")
	server = strings.TrimPrefix(server, "https://")
	server = strings.TrimPrefix(server, "http://")
	server = strings.TrimPrefix(server, "wss://")
	server = strings.TrimPrefix(server, "ws://")
	if slash := strings.IndexByte(server, '/'); slash >= 0 {
		server = server[:slash]
	}
	wsURL := ""
	if server != "" {
		wsURL = "wss://" + server + "/ws/calls"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"server": server,
		"ws_url": wsURL,
	})
}

// 列出所有日期目录（如 2025-10-13）
func listDirs(w http.ResponseWriter, r *http.Request) {
	var dirs []string

	err := filepath.WalkDir(conf.System.RecoderFilePath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(conf.System.RecoderFilePath, path)
		if err != nil || rel == "." {
			return nil
		}
		// 简单判断是否为 YYYY-MM-DD 格式
		if len(rel) == 10 && rel[4] == '-' && rel[7] == '-' {
			dirs = append(dirs, rel)
		}
		return nil
	})

	if err != nil {
		http.Error(w, "扫描目录失败", http.StatusInternalServerError)
		return
	}

	// 按日期排序（升序）
	//sort.Strings(dirs)

	// 按日期排序（降序：最新日期在前）
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(dirs)
}

func listFilesInDir(w http.ResponseWriter, r *http.Request) {
	dirName := strings.TrimPrefix(r.URL.Path, "/dir/")
	dirPath := filepath.Join(conf.System.RecoderFilePath, dirName)

	if _, err := os.Stat(dirPath); os.IsNotExist(err) {
		http.Error(w, "目录不存在", http.StatusNotFound)
		return
	}

	var files []AudioFile
	err := filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		if !strings.HasSuffix(strings.ToLower(info.Name()), ".wav") {
			return nil
		}

		tm, err := parseTimeFromFilename(info.Name())
		if err != nil {
			log.Printf("跳过文件 %s: %v", info.Name(), err)
			return nil
		}

		// ✅ 正确构造 URL：/recordings/2025-10-14/filename.wav
		urlPath := "/recordings/" + dirName + "/" + info.Name()

		files = append(files, AudioFile{
			Name:      info.Name(),
			Timestamp: tm.Format("2006-01-02 15:04:05"),
			URL:       urlPath, // ✅ 使用正确路径
		})
		return nil
	})

	if err != nil {
		http.Error(w, "读取目录失败", http.StatusInternalServerError)
		return
	}

	// 按时间排序
	sort.Slice(files, func(i, j int) bool {
		ti, _ := time.Parse("2006-01-02 15:04:05", files[i].Timestamp)
		tj, _ := time.Parse("2006-01-02 15:04:05", files[j].Timestamp)
		return tj.Before(ti)
	})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}
