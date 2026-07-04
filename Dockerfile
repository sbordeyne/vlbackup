FROM gcr.io/distroless/static:nonroot
ARG TARGETPLATFORM
ENTRYPOINT ["/usr/bin/vlbackup"]
COPY $TARGETPLATFORM/vlbackup /usr/bin/
