package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/PuerkitoBio/goquery"
	"github.com/remeh/sizedwaitgroup"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
)

type Config struct {
	CookieCsrf     string `json:"cookie_csrf"`
	CookieUser     string `json:"cookie_user"`
	CookieSession  string `json:"cookie_session"`
	DownloadFolder string `json:"download_folder"`
	MaxDownloads   int    `json:"max_downloads"`
}

var CATEGORY = ``
var CONFIG Config

const domain = `https://www.racedepartment.com`

var downloadedSlugs map[string]interface{}

func init() {
	trace := flag.Bool("trace", false, "sets log level to trace")
	category := flag.String("category", "tracks", "type of download: tracks/cars, defaults to tracks")
	jsonlogs := flag.Bool("jsonlogs", false, "sets log format to json")
	flag.Parse()
	CATEGORY = *category
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	if *trace {
		zerolog.SetGlobalLevel(zerolog.TraceLevel)
	}
	if *jsonlogs == false {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}

	d, e := ioutil.ReadFile("config.json")
	if e != nil {
		log.Fatal().Err(e).Msg("error reading config file")
	}
	e = json.Unmarshal(d, &CONFIG)
	if e != nil {
		log.Fatal().Err(e).Msg("error loading config file")
	}

	if CONFIG.CookieCsrf == "" || CONFIG.CookieUser == "" || CONFIG.CookieSession == "" {
		log.Fatal().Msg("missing cookie config!!")
	}

	_ = os.MkdirAll(CONFIG.DownloadFolder, 0644)

	dl, e := ioutil.ReadFile(`downloaded.log`)
	if e != nil {
		log.Trace().Msg("no downloaded.log file exists")
	} else {
		downloadedSlugs = make(map[string]interface{})
		for _, v := range strings.Split(string(dl), "\n") {
			slug := strings.TrimSpace(v)
			if len(slug) > 0 {
				downloadedSlugs[slug] = nil
			}
		}
	}

	log.Info().
		Str("Category", CATEGORY).
		Int("Max Downloads", CONFIG.MaxDownloads).
		Str("Download Folder", CONFIG.DownloadFolder).
		Int("Already downloaded", len(downloadedSlugs)).
		Msg("Config loaded")
}

func GetListingURL(pageNumber int) (url string) {
	path := `ac-tracks.8`
	if CATEGORY == `cars` {
		path = `ac-cars.6`
	}

	url = fmt.Sprintf(`%s%s%s/`, domain, `/downloads/categories/`, path)
	if pageNumber > 1 {
		url = fmt.Sprintf(`%s?page=%v`, url, pageNumber)
	}
	return
}

func GetDetailURL(slug string) (url string) {
	return fmt.Sprintf(`%s/downloads/%sdownload`, domain, slug)
}

func Download(url string) io.ReadCloser {
	log.Trace().Str("url", url).Msg("Downloading")
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authority", "www.racedepartment.com")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9,es;q=0.8")
	req.Header.Set("Cookie", fmt.Sprintf("xf_csrf=%s; xf_user=%s; xf_session=%s", CONFIG.CookieCsrf, CONFIG.CookieUser, CONFIG.CookieSession))
	req.Header.Set("Referer", "https://www.racedepartment.com/downloads/1936-delage-15s8-dick-seaman-special.58017/")
	req.Header.Set("Sec-Ch-Ua", "\"Not_A Brand\";v=\"99\", \"Google Chrome\";v=\"109\", \"Chromium\";v=\"109\"")
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", "\"Linux\"")
	req.Header.Set("Sec-Fetch-Dest", "image")
	req.Header.Set("Sec-Fetch-Mode", "no-cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/109.0.0.0 Safari/537.36")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Err(err).Str("URL", url).Msg("Downloading")
	}
	return resp.Body
}

func getFileExtension(data []byte) string {
	if strings.HasPrefix(string(data[0:4]), `7z`) {
		return `7z`
	}
	if strings.HasPrefix(string(data[0:4]), `PK`) {
		return `zip`
	}
	if strings.HasPrefix(string(data[0:4]), `Rar`) {
		return `rar`
	}
	log.Error().Str("header", string(data[0:4])).Msg("Unknown filetype")
	return `unknown`
}

func main() {
	pageNumber := 1
	var detailURLs []string
	for {
		url := GetListingURL(pageNumber)
		response := Download(url)
		doc, err := goquery.NewDocumentFromReader(response)
		if err != nil {
			log.Fatal().Err(err).Str("url", url).Msg("Parsing response")
		}

		links := doc.Find(`div.structItem-title a`)

		links.Each(func(i int, s *goquery.Selection) {
			if detailPage, hasDetailPage := s.Attr(`href`); hasDetailPage {
				foundPage := strings.TrimPrefix(detailPage, `/downloads/`)
				detailURLs = append(detailURLs, foundPage)
				log.Trace().Str("slug", foundPage).Msg("Found slug")
			}
		})
		if links.Size() < 20 {
			break
		} else {
			pageNumber++
		}
	}

	log.Info().Msg(`Starting download!`)

	downloadedChan := make(chan string)
	go func() {
		for {
			slug := <-downloadedChan
			f, err := os.OpenFile("downloaded.log",
				os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
			if err != nil {
				log.Err(err).Msg("Cant write to downloaded.log")
			}

			if _, err := f.WriteString(slug + "\n"); err != nil {
				log.Err(err).Msg("Cant write to downloaded.log")
			}
			_ = f.Close()
		}
	}()

	swg := sizedwaitgroup.New(CONFIG.MaxDownloads)
	for _, slug := range detailURLs {
		if _, ok := downloadedSlugs[slug]; ok {
			log.Trace().Str("slug", slug).Msg("Already downloaded")
			continue
		}
		swg.Add()
		go func(slug string) {
			defer swg.Done()
			urlToDownload := GetDetailURL(slug)
			resp := Download(urlToDownload)
			data, e := ioutil.ReadAll(resp)
			if e != nil {
				log.Err(e).Str("url", urlToDownload).Msg("")
				return
			}
			filename := strings.TrimPrefix(slug, `/downloads/`)
			filename = strings.TrimSuffix(filename, `/`)
			filename = fmt.Sprintf(`%s/%s.%s`, CONFIG.DownloadFolder, filename, getFileExtension(data))
			e = ioutil.WriteFile(filename, data, 0644)
			if e != nil {
				log.Err(e).Str("filename", filename).Msg("Writing file to disk")
			}
			downloadedChan <- slug
		}(slug)
	}
	swg.Wait()
	log.Info().Msg("Finished downloading!")
}
