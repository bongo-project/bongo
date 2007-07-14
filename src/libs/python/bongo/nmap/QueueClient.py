import logging
import bongo, libbongo.libs
from CommandStream import *
from StoreConnection import StoreConnection

class RoutingFlags:
    NoFlags = 0
    Success = (1 << 0)
    Timeout = (1 << 1)
    Failure = (1 << 2)
    Header = (1 << 3)
    Body = (1 << 4)

class QueueClient:
    def __init__(self, user):
        self.connection = StoreConnection("localhost", 8670)
        self.stream = self.connection.stream

    def Abort(self):
        self.stream.Write("QABRT")
        r = self.stream.GetResponse()
        if r.code != 1000:
            raise bongo.BongoError(r.message)
        return r

    def Create(self):
        self.stream.Write("QCREA")
        r = self.stream.GetResponse()
        if r.code != 1000:
            raise bongo.BongoError(r.message)
        return r

    def Diskspace(self):
        self.stream.Write("QDSPC")
        r = self.stream.GetResponse()
        if r.code != 1000:
            raise bongo.BongoError(r.message)
        (bytes, junk) = r.message.split(" ", 1)
        return int(bytes)

    def Quit(self):
        self.stream.Write("QUIT")
        self.stream.close()

    def Run(self):
        self.stream.Write("QRUN")
        r = self.stream.GetResponse()
        if r.code != 1000:
            raise bongo.BongoError(r.message)
        return r

    def StoreFrom(self, sender, authAddress="-"):
        command = "QSTOR FROM %s %s" % (sender, authAddress)
        self.stream.Write(command)
        r = self.stream.GetResponse()
        if r.code != 1000:
            raise bongo.BongoError(r.message)
        return r

    def StoreLocal(self, recipient, origAddress, routeFlags=RoutingFlags.NoFlags):
        command = "QSTOR LOCAL %s %s %d" % (recipient, origAddress, routeFlags)
        self.stream.Write(command)
        r = self.stream.GetResponse()
        if r.code != 1000:
            raise bongo.BongoError(r.message)
        return r

    def StoreMessage(self, data):
        self.stream.Write("QSTOR MESSAGE %d" % (len(data)))
        self.stream.WriteRaw(data)

        r = self.stream.GetResponse()
        if r.code != 1000:
            raise bongo.BongoError(r.message)

        return r

    def StoreRaw(self):
        pass

    def StoreTo(self, recipient, origAddress, routeFlags=RoutingFlags.NoFlags):
        command = "QSTOR TO %s %s %d" % (recipient, origAddress, routeFlags)
        self.stream.Write(command)
        r = self.stream.GetResponse()
        if r.code != 1000:
            raise bongo.BongoError(r.message)
        return r
