# Dockerfile COPIED FROM https://github.com/jamaljsr/polar (docker/eclair/Dockerfile),
# which is itself derived from the official https://github.com/ACINQ/eclair Dockerfile.
# Changes from Polar: base images follow ACINQ's Dockerfile (Temurin 21 on
# Ubuntu) instead of Alpine, because eclair's secp256k1 JNI library is built
# against glibc and segfaults under musl; the data dir is /home/eclair/.eclair.
FROM eclipse-temurin:21-jdk-noble AS BUILD

# Let's fetch eclair dependencies, so that Docker can cache them
# This way we won't have to fetch dependencies again if only the source code changes
# The easiest way to reliably get dependencies is to build the project with no sources

# Pull source code from github instead of a local repo
RUN apt-get update && apt-get install -y git && rm -rf /var/lib/apt/lists/*

WORKDIR /usr/repo
ARG ECLAIR_VERSION
RUN git clone --depth=1 --branch=v${ECLAIR_VERSION} https://github.com/ACINQ/eclair.git .
# Let's fetch eclair dependencies, so that Docker can cache them
WORKDIR /usr/src
RUN cp -r /usr/repo/mvnw mvnw
RUN cp -r /usr/repo/.mvn .mvn
RUN cp /usr/repo/pom.xml pom.xml
RUN mkdir -p eclair-core && cp /usr/repo/eclair-core/pom.xml eclair-core/pom.xml
RUN mkdir -p eclair-front && cp /usr/repo/eclair-front/pom.xml eclair-front/pom.xml
RUN mkdir -p eclair-node && cp /usr/repo/eclair-node/pom.xml eclair-node/pom.xml
RUN mkdir -p eclair-node/modules && cp /usr/repo/eclair-node/modules/assembly.xml eclair-node/modules/assembly.xml
RUN mkdir -p eclair-core/src/main/scala && touch eclair-core/src/main/scala/empty.scala
# Blank build. We only care about eclair-node, and we use install because eclair-node depends on eclair-core
ENV MAVEN_OPTS=-Xmx512m -XX:MaxPermSize=1024m
RUN ./mvnw install -pl eclair-node -am
RUN ./mvnw clean
# Only then do we copy the sources
RUN cp -R -f /usr/repo/* .

# And this time we can build in offline mode, specifying 'notag' instead of git commit
RUN ./mvnw package -pl eclair-node -am -DskipTests -Dgit.commit.id=notag -Dgit.commit.id.abbrev=notag -o
# It might be good idea to run the tests here, so that the docker build fail if the code is bugged

FROM eclipse-temurin:21-jre-noble
WORKDIR /app

# install jq for eclair-cli
RUN apt-get update && apt-get install -y bash jq curl unzip gosu && rm -rf /var/lib/apt/lists/*

# noble ships a default "ubuntu" user with UID 1000, which collides with the
# host-matching eclair user the entrypoint creates.
RUN userdel -r ubuntu

# copy and install eclair-cli executable
COPY --from=BUILD /usr/src/eclair-core/eclair-cli .
RUN chmod +x eclair-cli && mv eclair-cli /sbin/eclair-cli

# we only need the eclair-node.zip to run
COPY --from=BUILD /usr/src/eclair-node/target/eclair-node-*.zip ./eclair-node.zip
RUN unzip eclair-node.zip && mv eclair-node-* eclair-node && chmod +x eclair-node/bin/eclair-node.sh

ENV ECLAIR_DATADIR=/home/eclair/.eclair
RUN chmod -R a+x eclair-node/*

RUN curl -SLO https://raw.githubusercontent.com/ACINQ/eclair/master/contrib/eclair-cli.bash-completion \
  && mkdir -p /etc/bash_completion.d \
  && mv eclair-cli.bash-completion /etc/bash_completion.d/ \
  && curl -SLO https://raw.githubusercontent.com/scop/bash-completion/master/bash_completion \
  && mkdir -p /usr/share/bash-completion/ \
  && mv bash_completion /usr/share/bash-completion/

COPY docker-entrypoint.sh /entrypoint.sh
COPY bashrc /home/eclair/.bashrc

RUN chmod a+x /entrypoint.sh

VOLUME ["/home/eclair/.eclair"]

EXPOSE 9735 8080

ENTRYPOINT ["/entrypoint.sh"]

CMD ["eclair-node"]
