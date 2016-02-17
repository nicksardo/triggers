FROM iron/base

RUN mkdir /app
WORKDIR /app
ADD triggers /app
CMD /app/triggers
