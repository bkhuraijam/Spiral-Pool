#!/bin/bash
set -e
CONF=/home/devault/.devault/devault.conf

# Render the config template on first boot only
if [ ! -f "$CONF" ]; then
  sed -e "s|\${RPC_USER}|${RPC_USER}|g" \
      -e "s|\${RPC_PASSWORD}|${RPC_PASSWORD}|g" \
      -e "s|\${ZMQ_PORT}|${ZMQ_PORT}|g" \
      /config/devault.conf.template > "$CONF"
  chmod 600 "$CONF"
fi

chown -R devault:devault /home/devault/.devault
exec gosu devault devaultd -conf="$CONF" -datadir=/home/devault/.devault
