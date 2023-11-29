package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
)

type paramCheck struct {
	url   string
	param string
}

var transport = &http.Transport{
	TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	DialContext: (&net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: time.Second,
		DualStack: true,
	}).DialContext,
}

var httpClient = &http.Client{
	Transport: transport,
}

func main() {
	banner := `

_   ____   __ _____ _____ 
| | / /\ \ / //  ___/  ___|
| |/ /  \ V / \ \--.\ \--. 
|    \  /   \  \--. \\--. \
| |\  \/ /^\ \/\__/ /\__/ /
\_| \_/\/   \/\____/\____/ 
                           
	to analyse...                           

`
	fmt.Println(color.YellowString(banner))
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	sc := bufio.NewScanner(os.Stdin)

	initialChecks := make(chan paramCheck, 40)

	appendChecks := makePool(initialChecks, func(c paramCheck, output chan paramCheck) {
		reflected, err := checkReflected(c.url)
		if err != nil {
			//fmt.Fprintf(os.Stderr, "error from checkReflected: %s\n", err)
			return
		}

		if len(reflected) == 0 {
			// TODO: wrap in verbose mode
			//fmt.Printf("no params were reflected in %s\n", c.url)
			return
		}

		for _, param := range reflected {
			output <- paramCheck{c.url, param}
		}
	})

	charChecks := makePool(appendChecks, func(c paramCheck, output chan paramCheck) {
		wasReflected, part, err := checkAppend(c.url, c.param, "iy3j4h234hjb23234")
		if err != nil {
			fmt.Fprintf(os.Stderr, "error from checkAppend for url %s with param %s: %s", c.url, c.param, err)
			return
		}

		if wasReflected {
			output <- paramCheck{c.url, c.param}
		}

		if part == "" {

		}
	})

	done := makePool(charChecks, func(c paramCheck, output chan paramCheck) {
		output_of_url := []string{c.url, c.param}
		var unfiltredChars []string
		for _, char := range []string{"\"", "'", "<", ">", "$", "|", "(", ")", "`", ":", ";", "{", "}"} {
			wasReflected, part, err := checkAppend(c.url, c.param, "aprefix"+char+"asuffix")
			if err != nil {
				fmt.Fprintf(os.Stderr, "error from checkAppend for url %s with param %s with %s: %s", c.url, c.param, char, err)
				continue
			}

			if wasReflected {
				unfiltredChars = append(unfiltredChars, char)
			}

			if part != "" {
				output_of_url = append(output_of_url, part)
			}
		}
		if len(output_of_url) >= 2 {
			if len(unfiltredChars) > 0 {

				fmt.Println(color.WhiteString("============================"))
				fmt.Println(color.GreenString("Severity: info"))
				fmt.Println(color.GreenString("URL: %s", output_of_url[0]))
				fmt.Println(color.CyanString("Param: %s", strings.Split(output_of_url[1], ":")[0]))
				fmt.Println(color.YellowString("Part: %s", output_of_url[2]))
				fmt.Println(color.YellowString("Unfiltreds: %s", unfiltredChars))
				fmt.Println()

			}
		}
	})

	for sc.Scan() {
		initialChecks <- paramCheck{url: sc.Text()}
	}

	close(initialChecks)
	<-done
}

func reflectValue(html string) string {
	// Expressão regular para encontrar tags de script e seu conteúdo
	patternScript := regexp.MustCompile(`<script\b[^>]*>(.*?)<\/script>`)

	// Procura por correspondências na string HTML
	matches := patternScript.FindAllStringSubmatch(html, -1)

	for _, match := range matches {
		if match[1] != "" && regexp.MustCompile("aprefix").MatchString(match[1]) {
			return "script"
		}
	}

	// Se o texto buscado não estiver dentro das tags de script, retorna "body"
	return "body"
}

func checkReflected(targetURL string) ([]string, error) {

	out := make([]string, 0)

	req, err := http.NewRequest("GET", targetURL, nil)
	if err != nil {
		return out, err
	}

	// temporary. Needs to be an option
	req.Header.Add("User-Agent", "User-Agent: Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/80.0.3987.100 Safari/537.36")

	resp, err := httpClient.Do(req)
	if err != nil {
		return out, err
	}
	if resp.Body == nil {
		return out, err
	}
	defer resp.Body.Close()

	// always read the full body so we can re-use the tcp connection
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return out, err
	}

	// nope (:
	if strings.HasPrefix(resp.Status, "3") {
		return out, nil
	}

	// also nope
	ct := resp.Header.Get("Content-Type")
	if ct != "" && !strings.Contains(ct, "html") {
		return out, nil
	}

	body := string(b)
	part := reflectValue(body)

	u, err := url.Parse(targetURL)
	if err != nil {
		return out, err
	}

	for key, vv := range u.Query() {
		for _, v := range vv {
			if !strings.Contains(body, v) {
				continue
			}

			out = append(out, key+":"+part)
		}
	}

	return out, nil
}

func checkAppend(targetURL, param, suffix string) (bool, string, error) {
	u, err := url.Parse(targetURL)
	param = strings.Split(param, ":")[0]
	if err != nil {
		return false, "", err
	}

	qs := u.Query()
	val := qs.Get(param)
	//if val == "" {
	//return false, nil
	//return false, fmt.Errorf("can't append to non-existant param %s", param)
	//}

	qs.Set(param, val+suffix)
	u.RawQuery = qs.Encode()

	reflected, err := checkReflected(u.String())
	if err != nil {
		return false, "", err
	}

	for _, r := range reflected {
		rp := strings.Split(r, ":")[0]
		part := strings.Split(r, ":")[1]

		if rp == param {
			return true, part, nil
		}
	}

	return false, "", nil
}

type workerFunc func(paramCheck, chan paramCheck)

func makePool(input chan paramCheck, fn workerFunc) chan paramCheck {
	var wg sync.WaitGroup

	output := make(chan paramCheck)
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			for c := range input {
				fn(c, output)
			}
			wg.Done()
		}()
	}

	go func() {
		wg.Wait()
		close(output)
	}()

	return output
}
