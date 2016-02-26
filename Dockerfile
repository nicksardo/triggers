FROM iron/base

RUN mkdir /app

# Uncomment if you want to include a config file
# ADD scale.json /app
ADD triggers /app

WORKDIR /app
CMD /app/triggers
