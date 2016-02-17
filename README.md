# Autoscale Triggers
An IronWorker that scales up other workers based on IronMQ queue sizes.

## Local Testing
If you do not have Go:
```shell
docker run --rm -it -v $PWD:/go/src/a -w /go/src/a -e "CONFIG_FILE=config.json" iron/go:dev sh -c "go get ./... && go run main.go"
```

If you have Go:
```shell
go get github.com/NickSardo/triggers
cd $GOPATH/src/github.com/NickSardo/triggers
export CONFIG_FILE=config.json
go run main.go
```

## Example configuration
```json
{
	"envs": {
		"q":{
    			"token": "AAA",
    			"project_id": "BBB",
                "host": "mq-aws-us-east-1-1.iron.io",
				"api_version": "3"
		},
		"w":{
    			"token": "AAA",
    			"project_id": "CCC"
		}
	},
	"alerts": [
			{
			"queueName": "sampleQueue",
			"queueEnv": "q",
			"workerName": "dequeuer",
			"workerEnv": "w",
			"triggers":[
				{
					"type":"ratio",
					"value": 10
				}
			],
			"cluster":"ABC"
		}
	],
	"cacheEnv":"w"
}
```

`envs`: named map of bjects describing different Iron.io environments. See dev.iron.io for more information  
`alerts`: Array of objects, each object connects a queue to monitor and a worker to start   
`queueEnv` or `workerEnv`: these values are found under defined environments above  
`cluster`: tasks for this worker are spawned on this cluster   
`cacheEnv`: this scaler code caches the last known queue size, provide an environment to Iron Cache  

#### Triggers
Triggers will tell the scaler how many tasks to spawn.  Given more than one trigger to an alert object, the max tasks generated among all triggers will be used.
###### `ratio`
For every VALUE messages on the queue, spawn 1 task.
###### `fixed`
If the queue size = exactly VALUE, spawn 1 task.  
###### `progressive`
If the queue size grows by VALUE messages since the last check time, spawn 1 task. Note that the check interval is adjustable in the code

## Deploying to IronWorker
If you want to skip compiling the code yourself, you can go to step 3 and use `nicksardo/triggers:0.1`

##### 1. Build this executable
```shell
docker run --rm -it -v $PWD:/go/src/a -w /go/src/a iron/go:dev sh -c "go get ./... && go build -o triggers"
```

##### 2. Build dockerfile and push to your docker registry
```shell
docker build -t {{youraccount}}/triggers:0.1 .
docker push {{youraccount}}/triggers:0.1
```

##### 3. Test again by creating a config file and running your docker image
```shell
vi prod_config.json  # create a config file called "prod_config.json" in an empty directory
docker run --rm -it -e "CONFIG_FILE=prod_config.json" -v $PWD:/app {{youraccount}}/triggers
```

##### 4. Register your docker image with Iron.io
```shell
# Upload
iron register {{youraccount}}/triggers:0.1
```

##### 5. Set configuration data
Go to HUD and copy/paste your prod_config.json contents to the worker configuration.


##### 6. Schedule task every 30 minutes
```
# Schedule this to run every 30 minutes via CLI or HUD
iron worker schedule -cluster default -run-every 1800 {{youraccount}}/triggers
```
