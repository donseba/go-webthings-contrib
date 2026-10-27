# WebThings contrib

Integrations for [go-webthings](../go-webthings) that depend on outside
services or libraries. The modules themselves (comments, newsletter,
registrations, …) live in go-webthings under `modules/`; this repository holds
what they connect to, so go-webthings itself stays free of these dependencies.

| Package | What it does |
| --- | --- |
| `mailjet` | Mailjet as `mail.Sender` (`Config.Mail`) and as list sync for the newsletter module (`newsletter.WithProvider`) |
| `s3` | Media storage on S3-compatible object stores: AWS S3, DigitalOcean Spaces, MinIO (`Config.Storage`); the app's readiness check (`/readyz`) checks that its bucket can be reached. Without `PublicURL` the site serves the files itself at `/media`, so the bucket can stay private; with it, links to the uploaded files, such as `<X>OriginalURL` in module views, go to the bucket or its CDN, whose files must then be publicly readable (`PublicRead` or a bucket policy). The resized copies pages show, such as a post's `ImageURL`, are always served by the site at `/media/v/`, from the bucket once they are made: in the background right after the upload, or on their first request |

```go
store, err := s3.New(s3.Config{Endpoint: "ams3.digitaloceanspaces.com", Bucket: "site", AccessKey: …, SecretKey: …})

app := webthings.New(webthings.Config{
    Mail:    mailjet.Sender{APIKey: …, SecretKey: …, From: "site@example.com", FromName: "Site"},
    Storage: store,
})

app.Use(newsletter.Module("letters", "Newsletter", newsletter.WithProvider(mailjet.List{APIKey: …, SecretKey: …, ListID: 123})))
```

## Rules

- Packages here may import go-webthings and its modules; go-webthings never
  imports this repository.
- A new outside dependency (a payment provider, an analytics service, a
  storage backend) belongs here, not in go-webthings.

## Development

```sh
task fmt        # like go-webthings
task lint
task test       # the S3 tests skip without a store
task s3:up      # a throwaway MinIO in Docker
task test:s3    # the S3 tests against it
task s3:down    # removes the MinIO
```

`fmt` and `lint` use go-webthings' pinned golangci-lint: run `task tools` in
`../go-webthings` first. `test:s3` sets `WT_TEST_S3`; set it yourself to test
against another S3-compatible store (see `s3/integration_test.go`).

`go.mod` requires a released go-webthings. To test contrib against a change
of go-webthings, build it in a Go workspace with a go-webthings checkout; see
[Development](../go-webthings/README.md#development) in go-webthings' README.
