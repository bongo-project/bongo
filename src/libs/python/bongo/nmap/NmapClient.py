import logging
from bongo.libs import msgapi
from CommandStream import *
from NmapConnection import NmapConnection
from cStringIO import StringIO
import bongo

__all__ = ["DocTypes",
           "DocFlags",
           "FlagMode",
           "NmapClient"]

class DocTypes:
    Unknown = 0x0001
    Mail = 0x0002
    Cal = 0x0003
    Addressbook = 0x0004

    Folder = 0x1000
    Shared = 0x2000
    SharedFolder = Folder | Shared
    Purged = 0x4000

class DocFlags:
    Read = (1 << 0)
    Deleted = (1 << 1)
    NewUIDL = (1 << 2)
    Escape = (1 << 3)
    PrioHigh = (1 << 4)
    PrioLow= (1 << 5)
    Purged= (1 << 6)
    Recent = (1 << 7) 
    Answered= (1 << 8)
    Draft = (1 << 9)
    MarkPurge = (1 << 10)
    Completed = (1 << 11)
    Private = (1 << 12)

class FlagMode:
    Show = 0
    Add = 1
    Remove = 2
    Replace = 3

class NmapClient:
    def __init__(self, host):
        self.connection = NmapConnection(host, 689)
        self.stream = self.connection.stream

    def _escape_quotes(self, s):
        return s.replace('"', '\\"')

    def Adbk(self, seqNum=None):
        self.stream.Write("ADBK LIST")
        r = self.stream.GetResponse()
        if r.code != 2002:
            raise CommandError(r)
        return AddressBookIterator(self.stream, r.message)

    def Cal(self, boxName):
        self.stream.Write("CAL %s", boxName)

        r = self.stream.GetResponse()
        if r.code != 1000:
            raise CommandError(r)

        return r

    def Csinfo(self, seqNum=None):
        if seqNum is None:
            self.stream.Write("CSINFO")
            r = self.stream.GetResponse()
            if r.code != 2002:
                raise CommandError(r)
            return CalendarInfoIterator(self.stream, r.message)

        else:
            self.stream.Write("CSINFO %s", seqNum)
            r = self.stream.GetResponse()
            if r.code != 2001:
                raise CommandError(r)
            return r

    def Cslist(self, seqNum):
        self.stream.Write("CSLIST %s ICAL", seqNum)

        r = self.stream.GetResponse()
        if r.code != 2023:
            raise CommandError(r)

        return iCalMessage(r, self.stream)

    def Csshow(self, pattern=None):
        if pattern == None:
            self.stream.Write("CSSHOW")
        else:
            self.stream.Write("CSSHOW %s", pattern)

        r = self.stream.GetResponse()
        if r.code != 2002:
            raise CommandError(r)

        return ResponseIterator(self.stream)

    def Delete(self, doc):
        self.stream.Write("DELETE %s", doc)
        r = self.stream.GetResponse()

        if r.code != 1000:
            raise CommandError(r)

    def Events(self, calendar=None, props=[], start=None, end=None):
        command = "EVENTS"

        if calendar is not None:
            command = command + " C" + calendar

        if len(props) > 0:
            command = command + " P" + ",".join(props)

        if start != None and end != None:
            command = command + " \"D%s-%s\"" % (start.strftime('%Y%m%dT%H%M%S'), end.strftime('%Y%m%dT%H%M%S'))

        self.stream.Write(command)
        return ItemIterator(self.stream, len(props))

    def Flag(self, guid, flags=None, mode=None):
        command = "FLAG %s" % guid

        if flags is None:
            mode = FlagMode.Show
        elif mode is None:
            mode = FlagMode.Replace
            command = "%s %d" % (command, flags)
        elif mode == FlagMode.Add:
            command = "%s +%d" % (command, flags)
        elif mode == FlagMode.Remove:
            command = "%s -%d" % (command, flags)

        self.stream.Write(command)
        r = self.stream.GetResponse()
        if r.code != 1000:
            raise CommandError(r)

        (old, new) = r.message.split(" ", 1)
        return (old, new)

    def Info(self, seqNum=None):
        if seqNum is None:
            self.stream.Write("INFO")
            r = self.stream.GetResponse()
            if r.code != 2002:
                raise CommandError(r)
            return MessageInfoIterator(self.stream, r.message)

        else:
            self.stream.Write("INFO %s", seqNum)
            r = self.stream.GetResponse()
            if r.code != 2001:
                raise CommandError(r)
            return r

    def List(self, seqNum):
        self.stream.Write("LIST %s", seqNum)

        r = self.stream.GetResponse()
        if r.code not in (2023, 2024):
            raise CommandError(r)

        return MailMessage(r, self.stream)

    def Mbox(self, boxName):
        self.stream.Write("MBOX %s", boxName)

        r = self.stream.GetResponse()
        if r.code != 1000:
            raise CommandError(r)

        return r

    def Mime(self, uid):
        command = "MIME %s" % uid

        self.stream.Write(command)

        parts = []

        r = self.stream.GetResponse()
        while r.code in (2002, 2003, 2004):
            parts.append(MimeItem(r))
            r = self.stream.GetResponse()

        if len(parts) == 0:
            return parts

        topPart = parts[0]

        if topPart.multipart:
            parent = topPart

            for child in parts[1:]:
                child.parent = parent

                if child.end:
                    parent = parent.parent
                    if parent is None:
                        break
                else:
                    parent.children.append(child)
                    if child.multipart:
                        parent = child

        if r.code != 1000:
            raise CommandError(r)

        return topPart

    def Nver(self):
        command = "NVER"

        self.stream.Write(command)
        r = self.stream.GetResponse()
        if r.code != 1000:
            raise CommandError(r)
        return r

    def Quit(self):
        self.stream.Write("QUIT")
        self.stream.close()

    def Rset(self):
        self.stream.Write("RSET")
        r = self.stream.GetResponse()
        if r.code != 1000:
            raise CommandError(r)
        return r

    def Show(self, pattern=None):
        if pattern == None:
            self.stream.Write("SHOW")
        else:
            self.stream.Write("SHOW %s", pattern)

        r = self.stream.GetResponse()
        if r.code != 2002:
            raise CommandError(r)

        return ResponseIterator(self.stream)

    def Ulist(self, user=None):
        if user == None:
            self.stream.Write("ULIST")
        else:
            self.stream.Write("ULIST %s", user)

        return ResponseIterator(self.stream)

    def User(self, name):
        self.stream.Write("USER %s", name)
        r = self.stream.GetResponse()
        if r.code != 1000:
            raise CommandError(r)
        return r

