import socket
import CommandStream
import bongo

import logging

def Auth(stream):
    """Authenticate against a Store connection on socket s"""
    from bongo.libs import msgapi

    r = stream.GetResponse()

    if r.code == 1000:
        # no authentication will be necessary
        return True
    elif r.code == 4242:
        greeting_salt = r.message

        hash = msgapi.NmapChallenge(greeting_salt)
        stream.Write("AUTH SYSTEM %s" % (hash))

        r = stream.GetResponse()

        if r.code == 1000:
            return True

    return False

def CookieAuth(stream, user, cookie):
    """Authenticate against a Store connection with a user cookie on socket s"""
    r = stream.GetResponse()

    stream.log.debug("SEND: AUTH COOKIE %s ****************" % user)
    stream.Write("AUTH COOKIE %s %s" % (user, cookie), nolog=1)

    r = stream.GetResponse()

    if r.code == 1000:
        return True

    return False

def UserAuth(stream, user, password):
    """Authenticate against a Store connection with a username and password"""
    r = stream.GetResponse()

    stream.log.debug("SEND: AUTH USER %s ********" % user)
    stream.Write("AUTH USER %s %s" % (user, password), nolog=1)

    r = stream.GetResponse()

    if r.code == 1000:
        return True

    return False

class StoreConnection:
    log = logging.getLogger("Bongo.StoreConnection")

    def __init__(self, host, port, systemAuth=True):
        self.Open(host, port)

        if systemAuth and not Auth(self.stream):
            raise bongo.BongoError("Failed authentication with store")

    def Open(self, host, port):
        self.log.debug("opening Store connection to %s:%d", host, port)

        s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        s.connect((host, port))
        self.socket = s

        s.settimeout(10)
        self.stream = CommandStream.CommandStream(s)

    def CookieAuth(self, user, cookie):
        return CookieAuth(self.stream, user, cookie)

    def UserAuth(self, user, password):
        return UserAuth(self.stream, user, password)

    def Close(self):
        self.stream.close()
