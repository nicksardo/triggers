# Autoscale Triggers

Example configuration
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
