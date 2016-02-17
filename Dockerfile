FROM iron/base

RUN mkdir /app
WORKDIR /app
ADD trigger /app
CMD /app/trigger
