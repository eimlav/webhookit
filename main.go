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
	"reflect"
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

// HookWrapper is used to track webhooks in the executeDestroy method
type HookWrapper struct {
	Hook        WebHook
	Duplicate   bool
	DestroySkip bool
	Destroy     bool
	Code        string
}

// canDestroy returns whether an item can be destroyed
// @return bool
func (d HookWrapper) canDestroy() bool {
	return d.Destroy && !d.DestroySkip
}

// ToString prints the string of a HookWrapper
func (d HookWrapper) ToString() string {
	output := ""
	if d.Duplicate {
		output += fmt.Sprint(Cyan(" [DUPLICATE]"))
	}
	if d.canDestroy() {
		output += fmt.Sprint(Brown(" [TO BE DESTROYED]"))
	}
	return d.Hook.StatusToString() + output
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

// Backups webhooks to a local JSON file
func backupWebHooks(filepath string, webHooks WebHooks) error {
	webHooksJSON, err := json.Marshal(webHooks)
	if err != nil {
		return err
	}
	return ioutil.WriteFile(filepath, webHooksJSON, 0644)
}

// Executes the backup functionality
// @return error
func executeBackup(filepath string, webHooks WebHooks) error {
	if filepath == "" {
		return nil
	}
	err := backupWebHooks(filepath, webHooks)
	if err != nil {
		return errors.New(fmt.Sprint(Red("Error backing up webhooks:"), Red(err)))
	}
	fmt.Println(fmt.Sprintf("%s %s\n", Magenta("Successfully backed up webhooks to"), Brown(filepath)))
	return nil
}

// Executes API requests to GitHub based on the options passed in
// @arg backupFlag string
// @return error
func executeCheck(backupFlag string) error {
	// Print title
	title := fmt.Sprintf("%s\n%s\n%s\n", Bold(Gray("* * * * * * * * * * * * * * * * * * * *")), Bold(Brown("             C H E C K")), Bold(Gray("* * * * * * * * * * * * * * * * * * * *")))
	fmt.Println(title)

	fmt.Println(Bold(Gray("Checking GitHub repo(s) for validity of webhooks...\n")))

	// Array containing indexes of duplicate hooks
	allWebHooks := WebHooks{}
	// Total output of hooks
	var totalOutput string

	// For each repo...
	for _, repo := range reposContainer.Repos {
		// Get web hooks
		webHooks, err := getWebHooks(repo.Name)
		if err != nil {
			fmt.Printf("%s %s\n\n", Red("Failed to retrieve web hooks:"), Red(err))
			continue
		}

		// Add webHooks to allWebHooks for backup
		if backupFlag != "" {
			allWebHooks.Hooks = append(allWebHooks.Hooks, webHooks.Hooks...)
		}

		// Convert WebHooks to map of HookWrappers
		hooksMap := make(map[string]*HookWrapper, len(webHooks.Hooks))
		for _, hook := range webHooks.Hooks {
			hooksMap[hook.URL] = &HookWrapper{
				Hook: hook,
				Code: strings.ToUpper(strconv.Itoa(hook.LastResponse.Code)),
			}
		}
		// For each hook...
		for index := range hooksMap {
			currentItem := hooksMap[index].Hook.URL

			for dIndex := range hooksMap {
				iterateItem := hooksMap[dIndex].Hook.URL
				// Skip the same hook or an item already marked as duplicate
				if currentItem == iterateItem || hooksMap[iterateItem].Duplicate == true {
					continue
				}

				// Check if hook is a duplicate
				if hooksMap[currentItem].Hook.Config.URL != "" && (hooksMap[currentItem].Hook.Config.URL == hooksMap[iterateItem].Hook.Config.URL) {
					// Mark hooks as duplicate
					hooksMap[currentItem].Duplicate = true
					hooksMap[iterateItem].Duplicate = true
				}
			}
		}

		// Print name of repo
		printName := fmt.Sprintf("%s\n\n", Bold(Magenta(repo.Name)))
		totalOutput += printName

		// Append each hook string ot totalOutput
		for _, hook := range hooksMap {
			totalOutput += hook.ToString() + "\n"
		}

		// Newline to space out each repo
		totalOutput += "\n"
	}

	// Execution of backup. Backup will only occur if a non-empty backupFlag is present
	if err := executeBackup(backupFlag, allWebHooks); err != nil {
		printError("Backup failed:", err)
	}

	// Print totalOutput
	fmt.Println(totalOutput)

	fmt.Println(Green("Check complete."))

	return nil
}

// Validates that the typesFlag passed in is a CSV list meeting the criteria
// of status codes in the form 3XX to 5XX.
// @arg typesFlag string - CSV string of types
// @return []string - Contains each type as a string
// @return error
func validateTypesFlag(typesFlag string) ([]string, error) {
	if strings.ToLower(typesFlag) == "none" {
		return []string{"N/A"}, nil
	}

	var failedTypes []string
	r, _ := regexp.Compile("\\d{3}|\\d[xX][xX]|\\d\\d[xX]")
	types := strings.Split(typesFlag, ",")

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
		if errorString != "" {
			return errors.New(errorString)
		}
	}
	return nil
}

// Compare two string arrays
// @return bool True if arrays are different, false if not or an error occurs
func compareStringArrays(arrayOne, arrayTwo []string) bool {
	// Check for nil arrays
	if (arrayOne == nil || arrayTwo == nil) || (len(arrayOne) != len(arrayTwo)) {
		return true
	}

	for i := range arrayOne {
		if arrayOne[i] != arrayTwo[i] {
			return true
		}
	}
	return false
}

// Output the diff of two webhooks and allow user to select one to return
// Marks hooks if user chooses to destroy them
// @arg hookOne WebHook
// @arg hookTwo WebHook
// @return error
func duplicateDiff(HookWrappers ...*HookWrapper) error {
	// Must have at least two hooks to compare
	if len(HookWrappers) < 1 {
		return nil
	}

	fmt.Println(Bold(Magenta("\n* * * * * * * * * * *\n   DUPLICATE FOUND\n* * * * * * * * * * *\n")))

	// Display diff of each WebHook
	hookRefs := make([]reflect.Value, len(HookWrappers))
	for index, HookWrapper := range HookWrappers {
		hookRefs[index] = reflect.ValueOf(HookWrapper.Hook)
	}

	// Gather diffs of each duplicate
	fieldCount := hookRefs[0].NumField()
	outputs := make([]string, fieldCount)

	// For each field of WebHook...
	for fieldIndex := 0; fieldIndex < fieldCount; fieldIndex++ {
		// Build output string by iterating over field of each item
		colourBool := true

		// Add name of field
		outputs[fieldIndex] = fmt.Sprintf("    %s\n", Bold(Gray(hookRefs[0].Type().Field(fieldIndex).Name)))

		// Add value of field for each hook
		for hookIndex := 0; hookIndex < len(hookRefs); hookIndex++ {
			if colourBool {
				outputs[fieldIndex] += fmt.Sprintf("     %s    %s\n", Brown(fmt.Sprint("- ", hookIndex, ":")), Brown(hookRefs[hookIndex].Field(fieldIndex).Interface()))
			} else {
				outputs[fieldIndex] += fmt.Sprintf("     %s    %s\n", Cyan(fmt.Sprint("- ", hookIndex, ":")), Cyan(hookRefs[hookIndex].Field(fieldIndex).Interface()))
			}
			colourBool = !colourBool
		}
	}

	// Print output of diffs
	for i := range outputs {
		fmt.Println(outputs[i])
	}

	// Accept user input to choose webhook to return
	fmt.Println(Bold(Gray("Select duplicates to remove (using a CSV string e.g. 0,1) or 'n' for none:")))

	// Used for user input
	var input string
	for {
		// Read input
		fmt.Scanln(&input)
		// Remove spaces and set uppercase
		input = strings.Replace(strings.ToUpper(input), " ", "", -1)
		// Split into array
		splitInput := strings.Split(input, ",")

		// Check if 'n' was selected
		if len(splitInput) == 1 && splitInput[0] == "N" {
			fmt.Printf("%s\n\n%s\n\n", Bold(Gray("You chose to destroy no duplicates")), Bold(Magenta("\n* * * * * * * * * * *\n        DONE\n* * * * * * * * * * *\n")))
			break
		}

		// int version of splitInput
		intSplitInput := make([]int, len(splitInput))

		// Check all options are valid
		valid := true
		for i := 0; i < len(splitInput); i++ {
			// Convert input to int
			intInput, err := strconv.Atoi(splitInput[i])
			if err != nil {
				valid = false
				break
			}

			// Break if input is not in range of possible options
			if intInput < 0 || intInput > len(hookRefs)-1 {
				valid = false
				break
			}

			intSplitInput[i] = intInput
		}

		// Check if input is valid
		if valid == false {
			fmt.Println(Red("Invalid choice. Please try again using CSV format."))
			continue
		}

		// Set destroy of each hook
		for _, hookIndex := range intSplitInput {
			HookWrappers[hookIndex].Destroy = true
		}

		// Print confirmation message and exit input loop
		output := strings.Join(splitInput, ",")
		fmt.Printf("%s %s\n\n%s\n\n", Bold(Gray("You chose option(s)")), Bold(Brown(output)), Bold(Magenta("\n* * * * * * * * * * *\n       DONE       \n* * * * * * * * * * *\n")))
		break
	}
	return nil
}

// Mark an array of HookWrappers as Duplicated
func markDuplicates(HookWrappers ...*HookWrapper) {
	for _, HookWrapper := range HookWrappers {
		HookWrapper.Duplicate = true
	}
}

// Executes the destroy process of webhooks
// @arg typesFlag string
// @arg duplicatesFlag bool
// @arg untriggeredFlag bool
// @arg listHooksToDestroyFlag bool
// @arg backupFlag string
// @return error
func executeDestroy(typesFlag string, duplicatesFlag, untriggeredFlag, listHooksToDestroyFlag bool, backupFlag string) error {
	// Print title
	title := fmt.Sprintf("%s\n%s\n%s\n", Bold(Gray("* * * * * * * * * * * * * * * * * * * *")), Bold(Brown("            D E S T R O Y")), Bold(Gray("* * * * * * * * * * * * * * * * * * * *")))
	fmt.Println(title)

	// Validate types
	types, err := validateTypesFlag(typesFlag)
	if err != nil {
		printError("Invalid type options specified:", err)
	}

	additionalOutput := ""
	if duplicatesFlag {
		additionalOutput += "and duplicates "
	}
	if untriggeredFlag {
		additionalOutput += "and untriggered webhooks"
	}
	fmt.Printf("%s %s %s\n", Bold(Gray("Webhooks to be destroyed with HTTP status codes matching")), Bold(Brown(types)), Bold(Brown(additionalOutput)))

	typesRegexString := convertTypesToRegex(types)

	fmt.Println(Bold(Gray("Checking GitHub repos for validity of webhooks and tagging those to destroy...\n")))

	// Array containing indexes of duplicate hooks
	allWebHooks := WebHooks{}
	// Total output of hooks
	var totalOutput string
	// Total output of hooks to destroy
	var totalDestroyOutput string
	// Array to store ID of all hooks to be destroyed
	var hooksToDestroy []string

	// For each repo...
	for _, repo := range reposContainer.Repos {
		// Get web hooks
		webHooks, err := getWebHooks(repo.Name)
		if err != nil {
			fmt.Printf("%s %s\n\n", Red("Failed to retrieve web hooks:"), Red(err))
			continue
		}

		// Add webHooks to allWebHooks for backup
		if backupFlag != "" {
			allWebHooks.Hooks = append(allWebHooks.Hooks, webHooks.Hooks...)
		}

		// Convert WebHooks to map of HookWrappers
		hooksMap := make(map[string]*HookWrapper, len(webHooks.Hooks))
		for _, hook := range webHooks.Hooks {
			hooksMap[hook.URL] = &HookWrapper{
				Hook: hook,
				Code: strings.ToUpper(strconv.Itoa(hook.LastResponse.Code)),
			}
		}
		// For each hook...
		for index := range hooksMap {
			currentItem := hooksMap[index].Hook.URL
			var duplicateHookWrappers []*HookWrapper
			duplicateHookWrappers = append(duplicateHookWrappers, hooksMap[index])

			for dIndex := range hooksMap {
				iterateItem := hooksMap[dIndex].Hook.URL
				// Skip the same hook or an item already marked as duplicate
				if currentItem == iterateItem || hooksMap[iterateItem].Duplicate == true {
					continue
				}

				// Check if hook is a duplicate
				if hooksMap[currentItem].Hook.Config.URL != "" && (hooksMap[currentItem].Hook.Config.URL == hooksMap[iterateItem].Hook.Config.URL) {
					// Mark hooks as duplicate
					hooksMap[currentItem].Duplicate = true
					hooksMap[iterateItem].Duplicate = true

					// Add iterate hook to duplicate hooks
					duplicateHookWrappers = append(duplicateHookWrappers, hooksMap[iterateItem])
				}
			}

			// Perform diff if duplicates found
			if len(duplicateHookWrappers) > 1 && duplicatesFlag {
				err := duplicateDiff(duplicateHookWrappers...)
				if err != nil {
					printError("Error occured generating duplicate diff:", err)
				}
			}

			// Check if hook should be destroyed
			typesRegex, err := regexp.Compile(typesRegexString)
			if err != nil {
				fmt.Printf("%s %s\n", Red("Error compiling types regex"), Red(err))
				continue
			}
			if typesRegex.MatchString(hooksMap[currentItem].Code) || (untriggeredFlag && hooksMap[currentItem].Code == "0") {
				hooksMap[currentItem].Destroy = true
			}
		}

		// Print name of repo
		printName := fmt.Sprintf("%s\n\n", Bold(Magenta(repo.Name)))
		totalOutput += printName
		totalDestroyOutput += printName

		// Determine which hooks to destroy then output all results
		for _, hook := range hooksMap {
			if hook.canDestroy() {
				totalDestroyOutput += fmt.Sprint(hook.Hook.URL, "\n")
				hooksToDestroy = append(hooksToDestroy, hook.Hook.URL)
			}
			//fmt.Println(hook.ToString())
			totalOutput += hook.ToString() + "\n"
		}

		// Newline to space out each repo
		totalOutput += "\n"
		totalDestroyOutput += "\n"
	}

	// Print totalOutput
	fmt.Println(totalOutput)

	// Return if no hooks to destroy were found
	hookCount := len(hooksToDestroy)
	if hookCount == 0 {
		fmt.Println(Green("Found no hooks to destroy."))
		return nil
	} else {
		fmt.Println(fmt.Sprintf("%s %d %s\n", Bold(Gray("Found")), Bold(Brown(hookCount)), Bold(Gray("hooks to destroy"))))
	}

	// If flag is true, print list of all hooks to be destroyed
	if listHooksToDestroyFlag {
		fmt.Printf("%s\n%s\n", Magenta("The following webhooks will be destroyed:\n"), totalDestroyOutput)
	}

	// Execution of backup. Backup will only occur if a non-empty backupFlag is present
	if err := executeBackup(backupFlag, allWebHooks); err != nil {
		printError("Backup failed:", err)
	}

	// Confirm with user to go ahead with destroys
	passPhrase := generatePassPhrase(8)
	fmt.Printf("%s %sEnter `%s` to continue or anything else to abort.\n", Bold("Do you wish to destroy the selected web hooks? Once done it"), Bold(Red("cannot be reverted.\n")), Brown(passPhrase))

	var input string

	fmt.Scanln(&input)
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
		backupFlag             string
	)

	// Parse options
	flag.StringVar(&filePath, "f", "", "File path of JSON file containing repos. Uses filepath as argument.")
	flag.StringVar(&repoFlag, "r", "", "A single specified repo using the syntax namespace/repo.")
	flag.BoolVar(&checkFlag, "c", false, "Check repos for broken webhooks.")
	flag.BoolVar(&destroyFlag, "d", false, "Destroy broken webhooks.")
	flag.StringVar(&typesFlag, "t", "3XX,4XX,5XX", "CSV list of HTTP status code types to destroy e.g. 2XX, 501 or 'none' to disable HTTP status code matching")
	flag.BoolVar(&duplicatesFlag, "ds", false, "Include duplicates webhooks when destroying.")
	flag.BoolVar(&untriggeredFlag, "u", false, "Include untriggered webhooks when destroying.")
	flag.BoolVar(&listHooksToDestroyFlag, "l", false, "List hooks to be destroyed before confirmation.")
	flag.StringVar(&backupFlag, "b", "", "Backups webhooks to JSON file. Uses filepath as argument.")
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
		executeCheck(backupFlag)
	case destroyFlag:
		executeDestroy(typesFlag, duplicatesFlag, untriggeredFlag, listHooksToDestroyFlag, backupFlag)
	}
}
