# webhookit
Tool built in Go for verifying the integrity of GitHub web hooks and providing the option to delete web hooks based off of multiple parameters.

## Usage
- `export WEBHOOKIT_API_KEY=<api-key>` Ensure api key has privileges to modify web hooks in your repositories.
- `go build`
- `./webhookit <action> [options]`

### Actions
- `--c`
    Check repos for broken webhooks. Cannot be used along with -destroy.
- `--d`
    Destroy broken webhooks. Cannot be used along with -check.

### Options
- `-f <string>`
    File path of JSON file containing repos. Uses filepath as argument. Cannot be used along with -repo.
- `-r <string>`
    A single specified repo using the syntax namespace/repo. Cannot be used along with -filepath.
- `-t <string>`
    CSV list of HTTP status code types to destroy e.g. 2XX, 501 e.g. 2XX, 501 or `none` to disable HTTP status code matching (default "3XX,4XX,5XX").
- `-b <string>`
    Backup webhooks to JSON file. Uses filepath as argument.
- `-ds`
    Include duplicates webhooks when destroying.
- `-l`
    List hooks to be destroyed before confirmation.
- `-u`
    Include untriggered webhooks when destroying.

### Repos JSON file syntax
```
{
    "repos": [
        {
            "name": "eimlav/api-testing"
        }
    ]
}
```