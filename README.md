# Cloud Run Docker Mirror

**Consider using [Remote
Repositories](https://cloud.google.com/artifact-registry/docs/repositories/remote-repo)
or [Virtual
Repositories](https://cloud.google.com/artifact-registry/docs/repositories/virtual-repo)
instead!**

Cloud Run Docker Mirror copies images from one Docker v2 Registry to another, as
a service. With this service, you can:

-   Mirror popular public images (e.g. `postgres`, `redis`) into your internal
    artifact registry.

-   Reduce rate limiting or costs against public Docker registries by pulling
    images infrequently and caching them locally.

-   Increase availability by reducing dependencies on third-party systems.

-   Improve build times by bring images onto a local or higher-bandwidth
    network.

-   Ensure consistency and enforce security practices/scanning without "getting
    in the way" of your developers.

Note that Cloud Run Docker Mirror is most applicable for _long-running_
mirroring where the upstream repository is expected to live indefinitely. For
one-time mirroring, consider using [`gcrane`][gcrane] or other migration tools
directly, without the overhead of a continuously running service.

The application is written in Go and is optimized for [Cloud Run][cloud-run]. It may work on other systems, but they are untested.

**This is not an official Google product.**


## Quick start

Suppose we want to mirror [postgres on the Docker Hub](https://hub.docker.com/_/postgres) to our internal Artifact Registry repository:

1.  Deploy the service to Cloud Run:

    ```sh
    gcloud run deploy "cloud-run-docker-mirror" \
      --quiet \
      --image "us-docker.pkg.dev/vargolabs/cloud-run-docker-mirror/server" \
      --platform "managed" \
      --project "${PROJECT_ID}" \
      --region "us-central1" \
      --timeout "30m"
    ```

1.  Invoke the service with a JSON payload:

    ```sh
    URL=$(gcloud run services describe "cloud-run-docker-mirror" --project "${PROJECT_ID}" --platform "managed" --region "us-central1" --format 'value(status.url)')

    curl "${URL}" -d '{"mirrors": [{"src":"postgres", "dst":"us-docker.pkg.dev/my-project/my-repo/postgres"}]}'
    ```

1.  Invoke this URL on a fixed schedule to mirror, perhaps using [Cloud Scheduler][cloud-scheduler].


## Production setup

This section details a full production setup using [Cloud Run][cloud-run],
[Cloud Scheduler][cloud-scheduler], and [Artifact Registry][artifact-registry].

1.  Install or update the [Cloud SDK][cloud-sdk] for your operating system.
    Alternatively, you can run these commands from [Cloud Shell][cloud-shell],
    which has the SDK and other popular tools pre-installed.

1.  Authenticate to the SDK:

    ```sh
    gcloud auth login --update-adc
    ```

    This will open a browser and prompt you to authenticate with your Google
    account.

1.  [Create a Google Cloud project][create-project], or use an existing one.

1.  Export your project ID as an environment variable. The rest of these
    instructions assumes this environment variable is set.

    ```sh
    export PROJECT_ID="my-project"
    ```

    Note this is your project _ID_, not the project _number_ or _name_.

1.  Enable the necessary Google APIs on the project - this only needs to be done
    once per project:

    ```sh
    gcloud services enable --project "${PROJECT_ID}" \
      appengine.googleapis.com \
      artifactregistry.googleapis.com \
      cloudscheduler.googleapis.com \
      run.googleapis.com
    ```

    This operation can take a few minutes, especially for recently-created
    projects.

1.  Deploy the service to Cloud Run:

    ```sh
    gcloud run deploy "cloud-run-docker-mirror" \
      --quiet \
      --image "us-docker.pkg.dev/vargolabs/cloud-run-docker-mirror/server" \
      --platform "managed" \
      --project "${PROJECT_ID}" \
      --region "us-central1" \
      --timeout "15m"
    ```

1.  Create a Service Account with permissions to invoke the Cloud Run service
    (this is required later):

    ```sh
    gcloud iam service-accounts create "docker-mirror-invoker" \
      --project "${PROJECT_ID}"
    ```

    ```sh
    gcloud run services add-iam-policy-binding "cloud-run-docker-mirror" \
      --quiet \
      --project "${PROJECT_ID}" \
      --platform "managed" \
      --region "us-central1" \
      --member "serviceAccount:docker-mirror-invoker@${PROJECT_ID}.iam.gserviceaccount.com" \
      --role "roles/run.invoker"
    ```

1.  Enable AppEngine (required for Cloud Scheduler):

    ```sh
    gcloud app create \
      --quiet \
      --project "${PROJECT_ID}" \
      --region "us-central"
    ```

1.  Get the URL of the deployed service:

    ```sh
    URL="$(gcloud run services describe "cloud-run-docker-mirror" \
      --quiet \
      --project "${PROJECT_ID}" \
      --platform "managed" \
      --region "us-central1" \
      --format 'value(status.url)')"
    ```

1.  Build the JSON body:

    ```sh
    BODY="$(cat <<EOF
    {
      "mirrors": [
        {
          "src": "postgres",
          "dst": "us-docker.pkg.dev/${PROJECT_ID}/mirrors/postgres"
        },
        {
          "src": "redis",
          "dst": "us-docker.pkg.dev/${PROJECT_ID}/mirrors/redis"
        }
      ]
    }
    EOF
    )"
    ```

1.  Create a Cloud Scheduler HTTP job to invoke the service daily at 7:00am:

    ```sh
    gcloud scheduler jobs create http "cloud-run-docker-mirror" \
      --project "${PROJECT_ID}" \
      --description "Mirror Docker repositories" \
      --uri "${URL}/" \
      --message-body "${BODY}" \
      --oidc-service-account-email "docker-mirror-invoker@${PROJECT_ID}.iam.gserviceaccount.com" \
      --schedule "0 7 * * *" \
      --time-zone="US/Eastern"
    ```

1.  _(Optional)_ Run the scheduled job now:

    ```sh
    gcloud scheduler jobs run "cloud-run-docker-mirror" \
      --project "${PROJECT_ID}"
    ```


Now use `us-docker.pkg.dev/${PROJECT_ID}/mirrors/postgres` instead of `postgres`
in your CI, deployments, etc!


## Request format

The request is a JSON request of the format:

```json
{
  "mirrors": [
    {
      "src": "...",
      "dst": "...",
    }
  ]
}
```

Where:

-   `mirrors` is an array of mirror requests

    - `src` is the source repository

    - `dst` is the destination repository


## Response format

The response is a JSON body of the format:

```json
{
  "ok": true | false,
  "errors": [
    {
      "index": 1,
      "name": "x to y",
      "error": "failed for some reason"
    }
  ]
}
```

Where:

-   `ok` indicates overall success or failure

-   `errors` is an array of errors

    - `index` is the index in the input request where the failure occurred

    - `name` is the name of the mirror

    - `error` is the actual error


## Building the image

The Cloud Run Docker Mirror image is hosted and is available at multiple source including Artifact Registry and GitHub Packages:

- us-docker.pkg.dev/vargolabs/cloud-run-docker-mirror/server
- europe-docker.pkg.dev/vargolabs/cloud-run-docker-mirror/server
- asia-docker.pkg.dev/vargolabs/cloud-run-docker-mirror/server
- gcr.io/vargolabs/cloud-run-docker-mirror/server
- docker.pkg.github.com/sethvargo/cloud-run-docker-mirror/server

If you would like to build and publish your own package:

1.  Clone this repository and change into that directory:

    ```sh
    git clone https://github.com/sethvargo/cloud-run-docker-mirror
    ```

    ```sh
    cd cloud-run-docker-mirror
    ```

1.  Build the container:

    ```sh
    docker build -t your-org/your-repo/your-image:tag .
    ```

1.  Push the container to your registry:

    ```sh
    docker push your-org/your-repo/your-image:tag
    ```


[artifact-registry]: https://cloud.google.com/artifact-registry
[cloud-run]: https://cloud.google.com/run
[cloud-scheduler]: https://cloud.google.com/scheduler/
[cloud-sdk]: https://cloud.google.com/sdk
[cloud-shell]: https://cloud.google.com/shell
[create-project]: https://cloud.google.com/resource-manager/docs/creating-managing-projects
[gcrane]: https://github.com/google/go-containerregistry/blob/master/cmd/gcrane/README.md
