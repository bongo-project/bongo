import socket
import CommandStream
import bongo, bongo.libs

import logging

def Auth(stream):
    """Authenticate against an NMAP connection on socket s"""
    r = stream.GetResponse()

    if r.code == 1000:
        # no authentication will be necessary
        return True
    elif r.code == 4242:
        greeting_salt = r.message

        hash = bongo.libs.msgapi.NmapChallenge(greeting_salt)
        stream.Write("AUTH %s", hash)

        r = stream.GetResponse()

        if r.code == 1000:
            return True

    return False

class NmapConnection:
    log = logging.getLogger("Bongo.NmapConnection")

    def __init__(self, host, port):
        self.Open(host, port)

    def Open(self, host, port):
        self.log.debug("opening NMAP connection to %s:%d", host, port)

        s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        s.connect((host, port))
        self.socket = s

        self.stream = CommandStream.CommandStream(s)

        if not Auth(self.stream):
            raise bongo.BongoError("Failed authentication with NMAP")
