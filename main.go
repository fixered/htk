package main

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/playwright-community/playwright-go"
	http "github.com/Danny-Dasilva/CycleTLS/cycletls"
)

const (
	URL_PRINCIPALE = "https://hattrick.ws/"
	LOGO          = "https://resource-m.calcionapoli24.it/www/thumbs/1200x/1590651555_987.jpg"
	MAX_WORKERS   = 5
	BROWSER_TIMEOUT = 45000
	PAGE_WAIT     = 3 * time.Second
)

var RENAME_RULES = []struct {
	keywords []string
	newName  string
}{
	{[]string{"1", "uno"}, "Sky Sport Uno"},
	{[]string{"calcio"}, "Sky Sport Calcio"},
	{[]string{"mix", "Mix"}, "Sky Sport Mix"},
	{[]string{"Max", "max"}, "Sky Sport Max"},
	{[]string{"arena"}, "Sky Sport Arena"},
	{[]string{"24"}, "Sky Sport 24"},
	{[]string{"tennis"}, "Sky Sport Tennis"},
	{[]string{"motogp", "moto gp"}, "Sky Sport MotoGP"},
	{[]string{"f1", "formula"}, "Sky Sport Formula 1"},
	{[]string{"dazn"}, "Dazn 1"},
}

type Canale struct {
	Nome     string
	URL      string
	Stream   string
	BaseName string
}

type BrowserPool struct {
	pw       *playwright.Playwright
	browsers []playwright.Browser
	mu       sync.Mutex
	index    int
}

func NewBrowserPool(size int) (*BrowserPool, error) {
	pw, err := playwright.Run()
	if err != nil {
		return nil, err
	}

	pool := &BrowserPool{
		pw:       pw,
		browsers: make([]playwright.Browser, size),
	}

	for i := 0; i < size; i++ {
		browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
			Headless: playwright.Bool(true),
			Args: []string{
				"--disable-blink-features=AutomationControlled",
				"--disable-dev-shm-usage",
				"--no-sandbox",
			},
		})
		if err != nil {
			pool.Close()
			return nil, err
		}
		pool.browsers[i] = browser
	}

	return pool, nil
}

func (p *BrowserPool) GetBrowser() playwright.Browser {
	p.mu.Lock()
	defer p.mu.Unlock()
	browser := p.browsers[p.index]
	p.index = (p.index + 1) % len(p.browsers)
	return browser
}

func (p *BrowserPool) Close() {
	for _, browser := range p.browsers {
		if browser != nil {
			browser.Close()
		}
	}
	if p.pw != nil {
		p.pw.Stop()
	}
}

func normalizzaNomeCanale(nome string) string {
	nomeL := strings.ToLower(nome)
	for _, rule := range RENAME_RULES {
		for _, k := range rule.keywords {
			if strings.Contains(nomeL, k) {
				return rule.newName
			}
		}
	}
	return strings.TrimSpace(nome)
}

func estraiNomeBase(nome string) string {
	nome = strings.ToLower(nome)
	nome = strings.ReplaceAll(nome, " hd", "")
	nome = strings.ReplaceAll(nome, " (backup)", "")
	nome = strings.ReplaceAll(nome, "(backup)", "")
	return strings.TrimSpace(nome)
}

func estraiCanali() []Canale {
	client := http.Init()

	response, err := client.Do(URL_PRINCIPALE, http.Options{
		Body:      "",
		Ja3:       "771,4865-4866-4867-49195-49199-49196-49200-52393-52392-49171-49172-156-157-47-53,0-23-65281-10-11-35-16-5-13-18-51-45-43-27-17513,29-23-24,0",
		UserAgent: "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36",
		Timeout:   15,
	}, "GET")

	if err != nil {
		fmt.Println("❌ ERRORE: hattrick.ws non raggiungibile -", err)
		return []Canale{}
	}

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(response.Body))
	if err != nil {
		fmt.Println("❌ ERRORE: parsing HTML fallito")
		return []Canale{}
	}

	canali := make([]Canale, 0, 50)
	doc.Find("button a[href$='.htm']").Each(func(i int, s *goquery.Selection) {
		href, _ := s.Attr("href")
		nome := normalizzaNomeCanale(s.Text())
		canali = append(canali, Canale{
			Nome:     nome,
			URL:      href,
			BaseName: estraiNomeBase(nome),
		})
	})

	return canali
}

func isPotentialStream(urlStr string) bool {
	if strings.Contains(urlStr, ".m3u8") || strings.Contains(urlStr, ".mpd") {
		return true
	}
	if strings.Contains(urlStr, "token=") {
		return true
	}
	
	urlLower := strings.ToLower(urlStr)
	cdnKeywords := []string{"planetary", "lovecdn", "cdn", "stream", "live", "hls", "fmp4", "manifest"}
	
	for _, keyword := range cdnKeywords {
		if strings.Contains(urlLower, keyword) && 
		   (strings.Contains(urlLower, "token") || strings.Contains(urlLower, ".m3u") || 
		    strings.Contains(urlLower, "playlist") || strings.Contains(urlLower, "index")) {
			return true
		}
	}
	return false
}

func estraiStreamURL(frameURL string) (string, error) {
	parsed, err := url.Parse(frameURL)
	if err != nil {
		return "", err
	}
	
	if strings.Contains(frameURL, ".m3u8") {
		return frameURL, nil
	}
	
	token := parsed.Query().Get("token")
	if token != "" {
		basePath := parsed.Path
		if idx := strings.LastIndex(basePath, "/"); strings.Contains(basePath, ".") && idx != -1 {
			basePath = basePath[:idx]
		}
		
		patterns := []string{"/index.fmp4.m3u8", "/index.m3u8", "/playlist.m3u8", "/master.m3u8"}
		for _, pattern := range patterns {
			streamURL := fmt.Sprintf("%s://%s%s%s?token=%s", parsed.Scheme, parsed.Host, basePath, pattern, token)
			return streamURL, nil
		}
	}
	
	return frameURL, nil
}

func apriEdEstraiStream(browser playwright.Browser, urlCanale string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(BROWSER_TIMEOUT)*time.Millisecond)
	defer cancel()

	bctx, err := browser.NewContext(playwright.BrowserNewContextOptions{
		UserAgent: playwright.String("Mozilla/5.0 (iPhone; CPU iPhone OS 16_0 like Mac OS X) AppleWebKit/605.1.15"),
	})
	if err != nil {
		return "", err
	}
	defer bctx.Close()

	var streamURLs []string
	var mu sync.Mutex
	found := make(chan string, 1)

	bctx.Route("**/*", func(route playwright.Route) {
		reqURL := route.Request().URL()
		
		if strings.Contains(reqURL, ".m3u8") {
			select {
			case found <- reqURL:
			default:
			}
		} else if isPotentialStream(reqURL) {
			mu.Lock()
			streamURLs = append(streamURLs, reqURL)
			mu.Unlock()
		}
		route.Continue()
	})

	page, err := bctx.NewPage()
	if err != nil {
		return "", err
	}

	page.Goto(urlCanale, playwright.PageGotoOptions{
		Timeout:   playwright.Float(float64(BROWSER_TIMEOUT)),
		WaitUntil: playwright.WaitUntilStateLoad,
	})

	select {
	case m3u8 := <-found:
		return m3u8, nil
	case <-time.After(PAGE_WAIT):
	case <-ctx.Done():
		return "", ctx.Err()
	}

	frames := page.Frames()
	for _, frame := range frames {
		frameURL := frame.URL()
		if frameURL != "about:blank" && frameURL != "" && isPotentialStream(frameURL) {
			if streamURL, err := estraiStreamURL(frameURL); err == nil && streamURL != "" {
				mu.Lock()
				streamURLs = append(streamURLs, streamURL)
				mu.Unlock()
			}
		}
	}

	mu.Lock()
	defer mu.Unlock()

	for _, streamURL := range streamURLs {
		if strings.Contains(streamURL, ".m3u8") {
			return streamURL, nil
		}
	}
	
	if len(streamURLs) > 0 {
		return streamURLs[0], nil
	}

	return "", fmt.Errorf("nessuno stream trovato")
}

func processaCanale(c Canale, pool *BrowserPool, risultati chan<- Canale, cache map[string]string, cacheMu *sync.RWMutex) {
	fmt.Printf("➡  %s (%s)\n", c.Nome, c.URL)

	cacheMu.RLock()
	cachedStream, exists := cache[c.BaseName]
	cacheMu.RUnlock()

	if exists {
		fmt.Printf("✅ %s - CACHE\n", c.Nome)
		c.Stream = cachedStream
		risultati <- c
		return
	}

	browser := pool.GetBrowser()
	stream, err := apriEdEstraiStream(browser, c.URL)
	if err != nil {
		fmt.Printf("❌ %s - %v\n", c.Nome, err)
		return
	}

	c.Stream = stream
	fmt.Printf("✓  %s - TROVATO\n", c.Nome)

	cacheMu.Lock()
	cache[c.BaseName] = stream
	cacheMu.Unlock()

	risultati <- c
}

func scriviM3U8(nomeFile string, canali []Canale) error {
	f, err := os.Create(nomeFile)
	if err != nil {
		return err
	}
	defer f.Close()

	f.WriteString("#EXTM3U\n\n")
	for i, c := range canali {
		fmt.Fprintf(f, "#EXTINF:-1 tvg-id=\"%d\" group-title=\"Sky Sport IPTV\" tvg-logo=\"%s\", %s\n%s\n\n",
			i+1, LOGO, c.Nome, c.Stream)
	}
	return nil
}

func main() {
	fmt.Println("📥 Raccolgo lista canali…")
	listaCanali := estraiCanali()

	if len(listaCanali) == 0 {
		fmt.Println("🚫 Nessun canale trovato. Uscita.")
		return
	}

	fmt.Printf("✅ Trovati %d canali\n", len(listaCanali))
	fmt.Println("🎬 Avvio browser pool e analisi parallela…")

	pool, err := NewBrowserPool(MAX_WORKERS)
	if err != nil {
		fmt.Println("❌ Errore inizializzazione browser pool:", err)
		return
	}
	defer pool.Close()

	cache := make(map[string]string)
	var cacheMu sync.RWMutex

	risultati := make(chan Canale, len(listaCanali))
	var wg sync.WaitGroup

	for _, c := range listaCanali {
		wg.Add(1)
		go func(canale Canale) {
			defer wg.Done()
			processaCanale(canale, pool, risultati, cache, &cacheMu)
		}(c)
	}

	go func() {
		wg.Wait()
		close(risultati)
	}()

	finali := []Canale{}
	visti := make(map[string]bool)

	for c := range risultati {
		if !visti[c.Stream] && c.Stream != "" {
			visti[c.Stream] = true
			finali = append(finali, c)
		}
	}

	if len(finali) == 0 {
		fmt.Println("\n❌ Nessuno stream valido trovato!")
		return
	}

	if err := scriviM3U8("hattrick.m3u8", finali); err != nil {
		fmt.Println("❌ Errore scrittura hattrick.m3u8:", err)
		return
	}

	fmt.Printf("\n🎉 COMPLETATO! (%d canali validi trovati)\n", len(finali))
	fmt.Println("📁 File creato: hattrick.m3u8")
}