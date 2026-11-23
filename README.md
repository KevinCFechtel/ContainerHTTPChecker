# ContainerHTTPChecker

Ein kleiner HTTP-/HTTPS-Healthcheck als statisches Go-Binary ohne externe
Go-Abhängigkeiten. Läuft auch in einem `scratch`-Container ohne Shell.

Der Check führt genau einen `GET` aus. HTTP-Status **200–299** bedeutet gesund.
Weiterleitungen werden nicht verfolgt; 3xx, 4xx, 5xx sowie Netzwerk-, Timeout-
und TLS-Fehler gelten als fehlgeschlagener Check. Der Response-Body wird nicht
ausgewertet.

## Konfiguration

| Umgebungsvariable | Standard | Bedeutung |
| --- | --- | --- |
| `HEALTHCHECK_URL` | erforderlich | Vollständige URL, z. B. `http://127.0.0.1:8080/health` |
| `HEALTHCHECK_TIMEOUT` | `5s` | Positiver Timeout für den gesamten Request, z. B. `500ms` oder `3s` |
| `HEALTHCHECK_CA_FILE` | nicht gesetzt | Pfad zu einer PEM-Datei mit zusätzlichen vertrauenswürdigen CA-Zertifikaten |

Erfolg bleibt ohne Ausgabe. Fehler werden auf stderr ausgegeben.

Exit-Codes: `0` = gesund, `1` = Check fehlgeschlagen oder ungültige Konfiguration.

## Lokal bauen

```sh
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o healthcheck .
HEALTHCHECK_URL=http://127.0.0.1:8080/health ./healthcheck
go test ./...
```

Für einen Linux-Container beim Bauen auf macOS:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/healthcheck .
```

Für ARM64 entsprechend `GOARCH=arm64` setzen.

## In ein bestehendes Anwendungsimage einbauen

Der Healthcheck muss **im Container der Anwendung** liegen. Ein separater
Checker-Container setzt nicht den Health-Status des Anwendungscontainers.
Beispiel für den letzten Abschnitt eines mehrstufigen Dockerfiles:

```dockerfile
# Die Stages "app-build" und "checker" müssen zuvor definiert sein.
# "checker" stellt /healthcheck und das CA-Bundle bereit, z. B. über
# FROM container-http-checker AS checker
FROM scratch
COPY --from=app-build /out/app /app
COPY --from=checker /healthcheck /healthcheck
# Nur für HTTPS erforderlich:
COPY --from=checker /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

ENV HEALTHCHECK_URL=http://127.0.0.1:8080/health
ENV HEALTHCHECK_TIMEOUT=5s
HEALTHCHECK --interval=30s --timeout=6s --start-period=5s --retries=3 CMD ["/healthcheck"]
ENTRYPOINT ["/app"]
```

Die JSON-/Exec-Form des Healthchecks braucht keine Shell. Das Binary liest die
Umgebungsvariablen selbst. Den Docker-Timeout etwas größer als
`HEALTHCHECK_TIMEOUT` wählen; bei längeren Timeouts beide Werte anpassen.

Alternativ in Docker Compose für ein Anwendungsimage, das das Binary enthält:

```yaml
services:
  app:
    image: meine-app:latest
    environment:
      HEALTHCHECK_URL: http://127.0.0.1:8080/health
      HEALTHCHECK_TIMEOUT: 5s
    healthcheck:
      test: ["CMD", "/healthcheck"]
      interval: 30s
      timeout: 6s
      start_period: 5s
      retries: 3
```

## Eigenes Checker-Image bauen

```sh
docker build -t container-http-checker .
docker run --rm -e HEALTHCHECK_URL=https://example.com container-http-checker
```

Das mitgelieferte Dockerfile erzeugt ein Scratch-Image mit statischem Binary
und CA-Bundle und verwendet einen unprivilegierten Benutzer. Ein normaler
Aufruf prüft einmal und beendet sich; das Image ist kein dauerhaft laufender
Dienst. Die enthaltene `HEALTHCHECK`-Anweisung ist vor allem als Vorlage für
das Anwendungsimage gedacht.

## HTTPS und eigene Zertifikate

Go prüft Zertifikatskette, Gültigkeitszeitraum und Hostnamen automatisch.
Das Betriebssystem des Hosts stellt einem Scratch-Container **keine**
vertrauenswürdigen CAs zur Verfügung. Deshalb kopiert das Dockerfile ein
CA-Bundle ins Image. Für reines HTTP reicht tatsächlich das Binary allein.

Für eine interne CA deren öffentliches CA-Zertifikat als PEM-Datei in den
Container kopieren oder read-only mounten und
`HEALTHCHECK_CA_FILE=/certs/internal-ca.pem` setzen. Die Datei muss für den
Containerbenutzer lesbar sein. Die CA wird zusätzlich zu den System-CAs
vertraut; das funktioniert auch ohne System-CA-Bundle. Private Schlüssel
werden nicht benötigt.

Der Hostname in `HEALTHCHECK_URL` muss zum Zertifikat passen — bei
`https://127.0.0.1` benötigt das Zertifikat einen passenden IP-SAN.
Es gibt bewusst keine Option zum Abschalten der Zertifikatsprüfung.
