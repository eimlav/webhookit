package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	. "github.com/logrusorgru/aurora"
)

var apiKey = os.Getenv("WEBHOOKIT_API_KEY")

const (
	requestDelay time.Duration = 50 * time.Millisecond
)

// ResponseJSON is the type representing an API response
type ResponseJSON []struct {
	data string
}

// Repo is the type representing a single repo
type Repo struct {
	Name string `json:"name"`
}

// ReposContainer is the type representing all repos
type ReposContainer struct {
	Repos []Repo `json:"repos"`
}

var reposContainer ReposContainer
var client = &http.Client{Timeout: 10 * time.Second}

// WebHooks is an array of WebHooks
type WebHooks struct {
	Hooks []WebHook
}

// WebHook is the type representing a single webhook in the form
// of what is returned from a GitHub API call
type WebHook struct {
	ID      int      `json:"id"`
	URL     string   `json:"url"`
	TestURL string   `json:"test_url"`
	PingURL string   `json:"ping_url"`
	Name    string   `json:"name"`
	Events  []string `json:"events"`
	Active  bool     `json:"active"`
	Config  struct {
		URL         string `json:"url"`
		ContentType string `json:"content_type"`
	} `json:"config"`
	UpdatedAt    time.Time `json:"updated_at"`
	CreatedAt    time.Time `json:"created_at"`
	LastResponse struct {
		Code    int    `json:"code"`
		Status  string `json:"status"`
		Message string `json:"message"`
	} `json:"last_response"`
}

// StatusToString returns a formatted string of the status of the web hook
func (w WebHook) StatusToString() (status string) {
	// Required for edge cases where w.Config.URL is empty
	var url string
	if w.Config.URL != "" {
		url = w.Config.URL
	} else {
		url = "No config url. Using name: " + w.Name
	}

	status = url + " => "
	codeString := strconv.Itoa(w.LastResponse.Code)

	switch {
	case codeString[0] == '2':
		status += fmt.Sprintf("%s | %s", Green(codeString), Green(w.LastResponse.Message))
	case codeString[0] == '0':
		status += fmt.Sprintf("%s | %s", Red(codeString), Red("Webhook has never been triggered"))
	default:
		status += fmt.Sprintf("%s | %s", Red(codeString), Red(w.LastResponse.Message))
	}

	return status
}

// retrieveRepos retrieves repository info from a local JSON file
// @arg filePath string - Absolute/relative file path of JSON file containing repos
func retrieveRepos(filePath string) {
	jsonFile, err := os.Open(filePath)
	if err != nil {
		printError("Issue opening repos file:", err)
	}
	defer jsonFile.Close()

	jsonBytes, _ := ioutil.ReadAll(jsonFile)
	jsonRepos := ReposContainer{}
	json.Unmarshal(jsonBytes, &jsonRepos)

	for _, value := range jsonRepos.Repos {
		reposContainer.Repos = append(reposContainer.Repos, Repo{
			value.Name,
		})
	}
}

// Check API key is valid
// @arg key string
// @return bool
func checkAPIKey(key string) bool {
	return len(key) > 0
}

// makeAPIRequest makes an API request to GitHub, passing any received data into output
// @arg requestURL string - API request url
// @arg httpType string - HTTP method to use
// @arg output interface{} - Object to output JSON response to
// @return error
func makeAPIRequest(requestURL, httpType string, output interface{}) error {
	// Build request
	request, err := http.NewRequest(httpType, requestURL, nil)
	if err != nil {
		return err
	}

	// Add authorisation token to header
	request.Header.Add("Authorization", "token "+apiKey)

	// Execute request
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode != 200 && response.StatusCode != 204 {
		return fmt.Errorf("%s %d %s", "HTTP Status Code", response.StatusCode, "returned")
	}

	return json.NewDecoder(response.Body).Decode(output)
}

// Retrieves webhooks for a specified repository
// @arg repoName string
// @return WebHooks Any webhooks found
// @return error
func getWebHooks(repoName string) (WebHooks, error) {
	var webHooks WebHooks

	// Build API request URL
	requestURL := "https://api.github.com/repos/" + repoName + "/hooks"
	httpType := "GET"

	// Execute request and check for errors
	err := makeAPIRequest(requestURL, httpType, &webHooks.Hooks)
	if err != nil {
		return WebHooks{}, fmt.Errorf("API Request Error : %s encountered error : %s", repoName, err)
	}

	return webHooks, nil
}

// Executes API requests to GitHub based on the options passed in
// @return error
func executeCheck() error {
	fmt.Println(Bold(Brown("* * * CHECK * * *")))
	fmt.Println(Bold(Gray("Checking GitHub repo(s) for validity of webhooks...\n")))

	// Array containing indexes of duplicate hooks
	//var duplicateHooksIndexFound []int
	// For each repo read in...
	for _, repo := range reposContainer.Repos {
		var duplicateHooksIndexFound []int
		// Print name of repo
		fmt.Println(Bold(Magenta(repo.Name)))
		// Get web hooks
		webHooks, err := getWebHooks(repo.Name)
		if err != nil {
			fmt.Printf("%s %s\n\n", Red("Failed to retrieve web hooks:"), Red(err))
			continue
		}

		// Check last_response of each web hook
		for index, hook := range webHooks.Hooks {
			output := hook.StatusToString()

			// Check for duplicates
			foundDuplicate := false
			for dIndex, dHook := range webHooks.Hooks {
				if dIndex == index {
					continue
				}
				if hook.Config.URL != "" && hook.Config.URL == dHook.Config.URL {
					if !contains(duplicateHooksIndexFound, dIndex) {
						duplicateHooksIndexFound = append(duplicateHooksIndexFound, index)
						foundDuplicate = true
					}
				}
			}
			if foundDuplicate {
				output += fmt.Sprintf("%s", Cyan(" [DUPLICATE] "))
			}

			fmt.Println(output)
		}

		// Create newline
		fmt.Println()

		// Sleep until next API requests
		time.Sleep(requestDelay)
	}

	fmt.Println(Green("Check complete."))

	return nil
}

// Validates that the typesFlag passed in is a CSV list meeting the criteria
// of status codes in the form 3XX to 5XX.
// @arg typesFlag string - CSV string of types
// @return []string - Contains each type as a string
// @return error
func validateTypesFlag(typesFlag string) ([]string, error) {
	types := strings.Split(typesFlag, ",")
	var failedTypes []string
	r, _ := regexp.Compile("\\d{3}|\\d[xX][xX]|\\d\\d[xX]")
	for index, aType := range types {
		if !r.MatchString(aType) {
			failedTypes = append(failedTypes, aType)
		}
		types[index] = strings.ToUpper(aType)
	}

	if len(failedTypes) > 0 {
		return types, fmt.Errorf("Invalid types found: %s", failedTypes)
	}

	return types, nil
}

// Takes a string array and replaces any instances of 'X' in each string with '\\d' then joins
// each string using '|'.
// @arg types []string
// @return string - Regex string
func convertTypesToRegex(types []string) string {
	var newTypes []string
	for _, aType := range types {
		newTypes = append(newTypes, strings.Replace(aType, "X", "\\d", -1))
	}
	return strings.Join(newTypes, "|")
}

// Generates random pass phrase
// @arg length int - Length of passphrase to generate
// @return string - The passphrase
func generatePassPhrase(length int) string {
	bytes := make([]byte, length)
	rand.Seed(time.Now().UTC().UnixNano())

	for i := 0; i < length; i++ {
		bytes[i] = byte(65 + rand.Intn(25))
	}
	return string(bytes)
}

// Checks an int array for an instance of a supplied int
// @arg array []int
// @arg input int
// @return bool
func contains(array []int, input int) bool {
	for _, item := range array {
		if item == input {
			return true
		}
	}
	return false
}

// Appends a string to a string array if the input string is not already present
// @arg array []string
// @arg input string
// @return []string
func uniqueAppend(array []string, input string) []string {
	for _, item := range array {
		if item == input {
			return array
		}
	}
	return append(array, input)
}

// Executes the destroy process of webhooks
// @arg typesFlag string
// @arg duplicatesFlag bool
// @arg untriggeredFlag bool
// @arg listHooksToDestroyFlag bool
// @return error
func executeDestroy(typesFlag string, duplicatesFlag, untriggeredFlag, listHooksToDestroyFlag bool) error {
	fmt.Println(Bold(Brown("* * * DESTROY * * *")))

	// Validate types
	types, err := validateTypesFlag(typesFlag)
	if err != nil {
		printError("Invalid type options specified:", err)
	}

	var duplicatesOutput string
	if duplicatesFlag {
		duplicatesOutput = "and duplicates"
	}
	fmt.Printf("%s %s %s\n", Bold(Gray("Webhooks to be destroyed with HTTP status codes matching")), Bold(Brown(types)), Bold(Brown(duplicatesOutput)))

	typesRegexString := convertTypesToRegex(types)

	fmt.Println(Bold(Gray("Checking GitHub repos for validity of webhooks and tagging those to destroy...\n")))

	// Complete output of hooks to destroy
	var totalOutput string
	// Array to store ID of all hooks to be destroyed
	var hooksToDestroy []string
	var temparray []string
	// For each repo..
	for _, repo := range reposContainer.Repos {
		// Array containing indexes of duplicate hooks
		var duplicateHooksIndexFound []int
		// Print name of repo
		fmt.Println(Bold(Magenta(repo.Name)))
		totalOutput += fmt.Sprint(Bold(Magenta(repo.Name)), "\n")

		// Get web hooks
		webHooks, err := getWebHooks(repo.Name)
		if err != nil {
			fmt.Printf("%s %s\n\n", Red("Failed to retrieve web hooks:"), Red(err))
			continue
		}

		// Check last_response of each web hook
		for index, hook := range webHooks.Hooks {
			output := hook.StatusToString()
			codeString := strings.ToUpper(strconv.Itoa(hook.LastResponse.Code))

			// Check for duplicates
			foundDuplicate := false
			for dIndex, dHook := range webHooks.Hooks {
				if dIndex == index {
					continue
				}
				if hook.Config.URL != "" && hook.Config.URL == dHook.Config.URL {
					if !contains(duplicateHooksIndexFound, dIndex) {
						if duplicatesFlag {
							hooksToDestroy = uniqueAppend(hooksToDestroy, hook.URL)
						}
						duplicateHooksIndexFound = append(duplicateHooksIndexFound, index)
						foundDuplicate = true
					}
				}
			}
			if foundDuplicate {
				output += fmt.Sprintf("%s", Cyan(" [DUPLICATE] "))
			}

			// Check if codeString matches any types supplied
			typesRegex, err := regexp.Compile(typesRegexString)
			if err != nil {
				fmt.Printf("%s %s\n", Red("Error compiling types regex"), Red(err))
				continue
			}
			if typesRegex.MatchString(codeString) || foundDuplicate && duplicatesFlag || untriggeredFlag && hook.LastResponse.Code == 0 {
				output += fmt.Sprintf("%s", Brown(" [TO BE DESTROYED] "))
				totalOutput += output + "\n"
				hooksToDestroy = uniqueAppend(hooksToDestroy, hook.URL)
				temparray = append(temparray, hook.Config.URL)
			}

			// Print result
			fmt.Println(output)
		}

		// Create newline
		fmt.Println()

		totalOutput += "\n"

		// Sleep until next API requests
		time.Sleep(requestDelay)
	}

	// Return if no hooks to destroy were found
	if len(hooksToDestroy) == 0 {
		fmt.Println(Green("Found no hooks to destroy."))
		return nil
	}

	// If flag is true, print list of all hooks to be destroyed
	if listHooksToDestroyFlag {
		fmt.Printf("%s\n%s\n", Magenta("The following webhooks will be destroyed:\n"), totalOutput)
	}

	// Confirm with user to go ahead with destroys
	passPhrase := generatePassPhrase(8)
	fmt.Printf("%s %sEnter `%s` to continue or anything else to abort.\n", Bold("Do you wish to destroy the selected web hooks? Once done it"), Bold(Red("cannot be reverted.\n")), Brown(passPhrase))
	var input string
	fmt.Scanln(&input)
	if err != nil {
		printError("Error reading input:", err)
	}
	input = strings.TrimSpace(strings.ToUpper(input))

	if input == passPhrase {
		if err := destroyWebHooks(hooksToDestroy); err != nil {
			printError("Error destroying all web hooks\n", err)
		} else {
			fmt.Println(Green("\nDestruction completed."))
		}
	} else {
		fmt.Println(Green("\nDestruction aborted."))
	}

	return nil
}

// Destroys a webhook using a supplied API URL
// @arg requestURL string
// @return error
func destroyWebHook(requestURL string) error {
	// Build request
	request, err := http.NewRequest("DELETE", requestURL, nil)
	if err != nil {
		return err
	}

	// Add authorisation token to header
	request.Header.Add("Authorization", "token "+apiKey)

	// Execute request
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode == 204 {
		return nil
	}
	return errors.New("Encountered error deleting " + requestURL)
}

// Destroys multiple webhooks using an array of API URLs
// @arg webHookURLs []string
// @return error
func destroyWebHooks(webHookURLs []string) error {
	errorString := ""
	for _, url := range webHookURLs {
		err := destroyWebHook(url)
		if err != nil {
			errorString += fmt.Sprintf("- %s %s : %s", Red("Error deleting web hook"), url, Red(err))
		}
		if url == "kjfhkjsdfh" {
			return nil
		}
		if errorString != "" {
			return errors.New(errorString)
		}
	}

	return nil
}

// Prints an error then exits
func printError(args ...interface{}) {
	fmt.Println(Red(args))
	os.Exit(1)
}

func main() {
	// Declare flag variables
	var (
		filePath               string
		repoFlag               string
		checkFlag              bool
		destroyFlag            bool
		typesFlag              string
		duplicatesFlag         bool
		untriggeredFlag        bool
		listHooksToDestroyFlag bool
	)

	// Parse options
	flag.StringVar(&filePath, "f", "", "File path of JSON file containing repos.")
	flag.StringVar(&repoFlag, "r", "", "A single specified repo using the syntax namespace/repo.")
	flag.BoolVar(&checkFlag, "c", false, "Check repos for broken webhooks.")
	flag.BoolVar(&destroyFlag, "d", false, "Destroy broken webhooks.")
	flag.StringVar(&typesFlag, "t", "3XX,4XX,5XX", "CSV list of HTTP status code types to destroy e.g. 2XX, 501.")
	flag.BoolVar(&duplicatesFlag, "ds", false, "Include duplicates webhooks when destroying.")
	flag.BoolVar(&untriggeredFlag, "u", false, "Include untriggered webhooks when destroying.")
	flag.BoolVar(&listHooksToDestroyFlag, "l", false, "List hooks to be destroyed before confirmation.")
	flag.Parse()

	// Validate options
	switch {
	case !(checkFlag || destroyFlag):
		printError("You must select an option: --c or --d")
	case checkFlag && destroyFlag:
		printError("You can only select one option")
	case (filePath != "") && (repoFlag != ""):
		printError("You can only specify either a file path or repo")
	}

	// Retrieve repos from JSON file
	if repoFlag != "" {
		reposContainer.Repos = append(reposContainer.Repos, Repo{repoFlag})
	} else {
		retrieveRepos(filePath)
	}

	// Check API key exists
	if !checkAPIKey(apiKey) {
		printError("API key not found.")
	}

	// Execute API requests
	switch {
	case checkFlag:
		executeCheck()
	case destroyFlag:
		executeDestroy(typesFlag, duplicatesFlag, untriggeredFlag, listHooksToDestroyFlag)
	}

}
