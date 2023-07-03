#!/bin/bash

sh -c 'irtt client -i $IRTT_INTERVAL -d $IRTT_DURATION -l $IRTT_LENGTH --tripm=$IRTT_TRIPM --fill=rand --sfill=rand -o client_data $IRTT_SERVER_IP'
sleep infinity
