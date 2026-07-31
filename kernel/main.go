//go:build tamago && arm64

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"runtime"
	"strings"

	guestvsock "github.com/spr-networks/spr-tamago-demo/kernel/vsock"
	"github.com/usbarmory/tamago/kvm/virtio"
)

const (
	vsockPort = 4040

	virtioMMIOStart = 0x0a002000
	virtioMMIOEnd   = 0x0a020000
	virtioMMIOStep  = 0x1000
)

var page = template.Must(template.New("index").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <title>SPR TamaGo Kernel Demo</title>
  <style>
    :root { color-scheme: dark; font-family: Inter, ui-sans-serif, system-ui, sans-serif; }
    * { box-sizing: border-box; }
    body { margin: 0; min-height: 100vh; display: grid; place-items: center; color: #eef7f2;
      background: radial-gradient(circle at 20% 15%, #164d45 0, transparent 32%), #071714; }
    main { width: min(820px, calc(100% - 32px)); padding: 40px; border: 1px solid #2d6358;
      border-radius: 20px; background: rgba(9, 31, 27, .94); box-shadow: 0 24px 80px #0008; }
    .eyebrow { color: #79e2bd; font-size: .8rem; font-weight: 700; letter-spacing: .16em; text-transform: uppercase; }
    h1 { margin: 12px 0 24px; font-size: clamp(2rem, 6vw, 4rem); line-height: 1; }
    .hello { padding: 20px; border-radius: 12px; background: #06110f; color: #b8ffdf;
      font: 600 clamp(.9rem, 2.5vw, 1.15rem)/1.55 ui-monospace, SFMono-Regular, Menlo, monospace; }
    dl { display: grid; grid-template-columns: max-content 1fr; gap: 12px 20px; margin: 28px 0 0; }
    dt { color: #8eaaa2; } dd { margin: 0; overflow-wrap: anywhere; } .ok { color: #79e2bd; }
    @media (max-width: 560px) { main { padding: 26px; } dl { grid-template-columns: 1fr; gap: 5px; } dd { margin-bottom: 9px; } }
  </style>
</head>
<body>
  <main>
    <div class="eyebrow">Direct-booted kernel · no Linux guest</div>
    <h1>Hello, SPR.</h1>
    <div class="hello">Hello World from the TamaGo kernel!</div>
    <dl>
      <dt>Runtime</dt><dd class="ok">{{.GOOS}}/{{.GOARCH}}</dd>
      <dt>Role</dt><dd>krun guest kernel</dd>
      <dt>SPR IPC</dt><dd>virtio-vsock · port {{.Port}}</dd>
      <dt>Network</dt><dd class="ok">{{.NetworkPhase}}</dd>
      <dt>Interface</dt><dd>virtio-net · {{.MAC}}</dd>
      <dt>DHCP address</dt><dd>{{.Address}}</dd>
      <dt>Gateway</dt><dd>{{.Gateway}}</dd>
      <dt>DNS</dt><dd>{{.DNS}}</dd>
      <dt>Internet probe</dt><dd>{{.Probe}}</dd>
      {{if .NetworkError}}<dt>Network detail</dt><dd>{{.NetworkError}}</dd>{{end}}
      <dt>Linux in VM</dt><dd>none</dd>
    </dl>
  </main>
</body>
</html>`))

type pageData struct {
	GOOS         string
	GOARCH       string
	Port         uint32
	NetworkPhase string
	MAC          string
	Address      string
	Gateway      string
	DNS          string
	Probe        string
	NetworkError string
}

func findVsockDevice() (*guestvsock.Device, uint32, error) {
	for base := uint32(virtioMMIOStart); base < virtioMMIOEnd; base += virtioMMIOStep {
		transport := &virtio.MMIO{Base: base}
		if transport.DeviceID() != guestvsock.DeviceID {
			continue
		}
		dev := &guestvsock.Device{Transport: transport}
		if err := dev.Init(); err != nil {
			return nil, base, err
		}
		return dev, base, nil
	}
	return nil, 0, fmt.Errorf("virtio-vsock device not found")
}

func httpResponse(status, contentType string, body []byte) []byte {
	header := fmt.Sprintf("HTTP/1.1 %s\r\nContent-Type: %s\r\nContent-Length: %d\r\nConnection: close\r\nX-TamaGo-Kernel: true\r\n\r\n", status, contentType, len(body))
	return append([]byte(header), body...)
}

func handleRequest(request []byte) []byte {
	lineEnd := bytes.Index(request, []byte("\r\n"))
	if lineEnd < 0 {
		return httpResponse("400 Bad Request", "text/plain; charset=utf-8", []byte("bad request\n"))
	}
	fields := strings.Fields(string(request[:lineEnd]))
	if len(fields) != 3 || fields[0] != "GET" {
		return httpResponse("405 Method Not Allowed", "text/plain; charset=utf-8", []byte("method not allowed\n"))
	}

	switch fields[1] {
	case "/status":
		network := networkStatusSnapshot()
		body := new(bytes.Buffer)
		_ = json.NewEncoder(body).Encode(map[string]any{
			"runtime": runtime.GOOS,
			"arch":    runtime.GOARCH,
			"role":    "kernel",
			"linux":   false,
			"ipc":     "virtio-vsock",
			"port":    vsockPort,
			"network": network,
		})
		return httpResponse("200 OK", "application/json", body.Bytes())
	case "/":
		network := networkStatusSnapshot()
		body := new(bytes.Buffer)
		if err := page.Execute(body, pageData{
			GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, Port: vsockPort,
			NetworkPhase: network.Phase, MAC: network.MAC, Address: network.Address,
			Gateway: network.Gateway, DNS: dnsText(network.DNS), Probe: network.Probe,
			NetworkError: network.Error,
		}); err != nil {
			return httpResponse("500 Internal Server Error", "text/plain; charset=utf-8", []byte("template error\n"))
		}
		return httpResponse("200 OK", "text/html; charset=utf-8", body.Bytes())
	default:
		return httpResponse("404 Not Found", "text/plain; charset=utf-8", []byte("not found\n"))
	}
}

func main() {
	log.SetFlags(0)
	log.Printf("Hello World from the TamaGo kernel! GOOS=%s GOARCH=%s", runtime.GOOS, runtime.GOARCH)

	dev, base, err := findVsockDevice()
	if err != nil {
		log.Fatal(err)
	}

	log.Printf("virtio-vsock MMIO=%#x CID=%d HTTP port=%d", base, dev.CID(), vsockPort)
	go startInternetNetworking()
	if err := dev.Serve(vsockPort, handleRequest); err != nil {
		log.Fatal(err)
	}
}
