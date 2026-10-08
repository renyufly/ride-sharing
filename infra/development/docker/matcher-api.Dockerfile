FROM alpine
WORKDIR /app

# ADD shared shared
# ADD build build

# ENTRYPOINT build/matcher-api

COPY --chmod=755 build/matcher-api /app/matcher-api

EXPOSE 8082

ENTRYPOINT ["/app/matcher-api"]