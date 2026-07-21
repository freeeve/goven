# goven

A fast Maven repository client in a single static binary. No JVM required.

goven fetches artifacts from Maven repositories (Nexus, Artifactory, Maven
Central) in milliseconds, using your existing `~/.m2/settings.xml` — servers,
mirrors, proxies, and profiles work exactly as they do with Maven, so there is
no configuration to migrate.

## Why

Plenty of CI jobs run Maven only to move artifacts around. Fetching a single
file with `mvn dependency:get` starts a JVM, loads Maven, and typically costs
several seconds and ~150 MB of memory — for what is fundamentally an HTTP GET
with checksums. For Python, JavaScript, and other non-Java projects that
publish to or consume from a Maven repository, that also means installing a
JDK and Maven into images that otherwise have no use for them.

Measured on the same machine and network (artifact purged, Maven's plugin
cache already warm), fetching `commons-lang3:3.14.0` from Maven Central:

|              | wall time | peak memory |
|--------------|-----------|-------------|
| `mvn dependency:get` | 5.5 s | 336 MB |
| `goven get`  | 1.3 s     | 14 MB       |

Same bytes, checksum-verified either way.

goven does the same repository operations natively:

- **Fast**: milliseconds instead of seconds; a few MB of memory instead of
  ~150 MB.
- **Zero config migration**: reads your `settings.xml` natively — server
  credentials, mirror rules (`mirrorOf`, including `*`, `external:*`, and
  `!` exclusions), proxies, profiles and profile activation (`-P`,
  `<activeProfiles>`, property-based).
- **Correct**: verifies `sha1`/`sha256` checksums and resolves SNAPSHOT
  timestamped versions from `maven-metadata.xml`, the way Maven does — not
  the way a hand-rolled curl script usually doesn't.
- **Small**: one static binary. Drop it into any CI image.

## Install

```sh
go install github.com/freeeve/goven@latest
```

Or download a release binary (coming soon).

## Usage

Fetch an artifact by coordinates (`groupId:artifactId:version[:type[:classifier]]`):

```sh
goven get org.apache.commons:commons-lang3:3.14.0
goven get com.example:my-lib:2.1.0-SNAPSHOT:jar:sources -o build/deps/
```

Deploy an artifact — a drop-in replacement for `mvn deploy:deploy-file`,
including SNAPSHOT timestamped versions, buildNumber increments, checksum
sidecars (md5/sha1/sha256/sha512), and `maven-metadata.xml` maintenance:

```sh
goven deploy build/lib.jar --gav com.example:lib:2.1.0 --repo nexus::https://nexus.corp/repository/releases
# or keep your existing mvn CLI line and just swap the command name:
goven deploy -Dfile=build/lib.jar -DgroupId=com.example -DartifactId=lib \
  -Dversion=2.1.0-SNAPSHOT -DrepositoryId=nexus -Durl=https://nexus.corp/repository/snapshots
```

Check your repository configuration — which settings files were loaded, which
profiles are active, which repositories (and mirrors) are in effect, and
whether they are reachable with your credentials:

```sh
goven doctor
goven -P nexus-prod doctor
```

Global flags, Maven-compatible where it counts:

```
-s <file>      user settings.xml (default ~/.m2/settings.xml)
-gs <file>     global settings.xml (default $M2_HOME/conf/settings.xml)
-P <profiles>  comma-separated profiles to activate (! to deactivate)
-Dkey=value    set a property (repeatable; feeds profile activation and
               interpolation)
```

## Status

`get`, `deploy`, and `doctor` are implemented and tested. Deploy metadata is
verified field-for-field against real `mvn deploy:deploy-file` output, and
repositories deployed by goven are consumed by stock Maven (round-trip tested
for both releases and timestamped SNAPSHOTs).

Not a Maven replacement — goven speaks the Maven *repository* protocol; it
does not run builds or plugins.

## Compatibility notes

- Profile activation supports `<activeByDefault>`, `<activeProfiles>`, `-P`
  (with `!` deactivation), and property-based activation. JDK/OS/file-based
  activation rules are not yet implemented.
- Encrypted passwords (`settings-security.xml`) are not yet supported; use
  plaintext or environment-variable interpolation (`${env.NAME}`) in
  `settings.xml`.
- One deliberate safety divergence: when deploying with a classifier, goven
  does not generate a POM by default (deploy-file's generated POM would land
  on the classifier-less path and overwrite the main artifact's POM). Pass
  `-DgeneratePom=true` to restore Maven's behavior.

## License

MIT
