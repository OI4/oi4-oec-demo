#/bin/bash

docker run -it -v `pwd`/certs:/mosquitto/certs -v `pwd`/passwd:/mosquitto/passwd -v `pwd`/mosquitto.conf:/mosquitto/config/mosquitto.conf -p 1883:1883 -p 8883:8883 eclipse-mosquitto:latest
