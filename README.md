# webhookit
Tool built in Go for verifying the integrity of GitHub web hooks and providing the option to delete web hooks based off of most recent HTTP status code returned.

## Usage
- `export WEBHOOKIT_API_KEY=<api-key>` Ensure api key has privileges to modify web hooks in your repositories.
- `go build`
- `./webhookit <action> [options]`

### Actions
- `-check`
    Check repos for broken webhooks. Cannot be used along with -destroy.
- `-destroy`
    Destroy broken webhooks. Cannot be used along with -check.

### Options
- `-filepath <string>`
    File path of JSON file containing repos. Cannot be used along with -repo.
- `-repo <string>`
    A single specified repo using the syntax namespace/repo. Cannot be used along with -filepath.
- `-types <string>`
    CSV list of HTTP status code types to destroy e.g. 2XX, 501 e.g. 2XX, 501 (default "3XX,4XX,5XX")