:exclamation: **:Archived:** You should use [buildx cache](https://docs.docker.com/engine/reference/commandline/buildx_build/#cache-from) :exclamation:

# guruguru-cache

CircleCI-like caching utility

**Caution (1): This tool is still under development. The spec might be changed in the near future.**

**Caution (2): This tool is not stable yet. When you try running, please do it in an isolated environment like a Docker container.**


## Problem

This is mainly for optimizing operations taking a long time like `bundle install` inside `docker build`.

When building a Docker image for Rails app, `bundle install` fetches & installs all of the gems from scratch. It takes a long time especially for native extension like `nokogiri`.

When the layer of `RUN bundle install` is cached by Docker, it's okay. But if `Gemfile.lock` is updated, the next `docer build` re-fetches & re-installs all of the gems again!

`guruguru-cache` stores and restores cache files to optimize installation.

## Usage

### Prerequisites

Cache files are stored into a bucket of Amazon S3.

You need to create a bucket and an IAM user having permissions for the bucket.

Access keys can be specified with environment variables defined by [AWS SDK for Go](https://docs.aws.amazon.com/ja_jp/sdk-for-go/v1/developer-guide/configuring-sdk.html).

In a basic case, you need to set these variables:

* `AWS_ACCESS_KEY_ID`
* `AWS_SECRET_ACCESS_KEY`
* `AWS_REGION`

### Installation

Currently, there are no binary releases. So you need to build by yourself or copying from a Docker image is useful.

```dockerfile
# In Dockerfile
COPY --from=yuyat/guruguru-cache /usr/local/bin/guruguru-cache /usr/local/bin
```

### Store cache

```
$ guruguru-cache store [flags] [cache key] [paths...]

Flags:
  -h, --help               help for store
      --s3-bucket string   S3 bucket to upload
```

#### Example

```
$ guruguru-cache store --s3-bucket=example-cache \
  'gem-v1-{{ arch }}-{{ checksum "Gemfile.lock" }}' \
  vendor/bundle
```

### Restore cache

```
$ guruguru-cache restore [flags] [cache keys...]

Flags:
  -h, --help               help for restore
      --s3-bucket string   S3 bucket to upload
```

#### Example

```
$ guruguru-cache restore --s3-bucket=example-cache \
  'gem-v1-{{ arch }}-{{ checksum "Gemfile.lock" }}' \
  'gem-v1-{{ arch }}'
```

### Cache key template

* `{{ checksum "FILEPATH" }}`: MD5 checksum of an arbitrary file
* `{{ arch }}`: CPU architecture
* `{{ epoch }}`: UNIX timestamp
* `{{ .Environment.FOO }}`: Environment variables
