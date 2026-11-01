FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/slotwatch /usr/bin/slotwatch
ENTRYPOINT ["/usr/bin/slotwatch"]
