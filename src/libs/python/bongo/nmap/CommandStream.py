import re
import logging
from bongo.BongoError import BongoError

__all__ = ["AddressBookItem",
           "AddressBookIterator",
           "CalendarInfoIterator",
           "CommandError",
           "CommandStream",
           "iCalMessage",
           "Item",
           "ItemIterator",
           "MailMessage",
           "MimeItem",
           "MessageInfoIterator",
           "Response",
           "ResponseIterator"]

class Response:
    def __init__(self, code, message, data=None):
        self.code = int(code)
        self.message = message
        self.data = data

class CommandError(BongoError):
    def __init__(self, response):
        BongoError.__init__(self, response.message)
        self.code = response.code

class Item:
    def __init__(self, response):
        # split on unescaped spaces
        ret = re.split(r"(?<!\\) ", response.message)

        # ditch the escaping backslashes
        ret = [re.sub(r"\\ ", " ", x) for x in ret]

        self.uid, self.type, self.flags, self.imapuid, self.created, self.bodylen, self.filename = ret

        self.flags = int(self.flags)

        self.props = {}

class MessageInfoItem:
    def __init__(self, response):
        self.seqNum, self.totalSize, self.headerSize, self.bodySize, self.flags, self.WUID, self.UID, self.mailboxID, self.msgDate = response.split()
        self.flags = int(self.flags)

class CalendarInfoItem:
    def __init__(self, response):
        self.seqNum, self.type, self.totalSize, self.iCalSize, self.flags, self.utcStart, self.utcEnd, self.utcDue, self.iCalSeqNum, self.recurrence, self.recurrenceID, self.UID, self.iCalUID, self.organizer, self.summary = re.findall(r'"[^"]*"|[^ ]+', response)
        self.flags = int(self.flags)
        self.organizer = self.organizer.strip('"')
        self.summary = self.summary.strip('"')

class AddressBookItem:
    def __init__(self, stream):
        r = stream.GetResponse()
        if r.code != 2001:
            raise CommandError(r)
        self.size, self.text = r.message.split(' ', 1)
        self.size = int(self.size)
        self.message = stream.Read(self.size)
        self.uid, self.email, self.firstName, self.lastName, self.phone1, self.phone1Type, self.phone2, self.phone2Type, self.bdayMonth, self.bdayDay, self.bdayYear, self.note = self.message.split('\r')

class MailMessage:
    def __init__(self, response, stream):
        self.headerSize, self.bodySize = response.message.split()
        self.headerSize = int(self.headerSize)
        self.bodySize = int(self.bodySize)
        self.message = stream.Read(self.headerSize + self.bodySize)

        r = stream.GetResponse()
        if r.code != 1000:
            raise CommandError(r)

class iCalMessage:
    def __init__(self, response, stream):
        self.size = response.message
        self.size = int(self.size)
        self.message = stream.Read(self.size)
        r = stream.GetResponse()
        if r.code != 1000:
            raise CommandError(r)

class MimeItem:
    def __init__(self, response):
        self.end = False
        self.children = []
        self.parent = None
        self.multipart = False

        if response.code == 2003 or response.code == 2004:
            self.end = True
            return

        self.type, self.subtype, self.charset, self.encoding, self.name, self.headerStart, self.headerLen, self.start, self.length, self.partHeaderLen, self.lines = re.findall(r'\".*\"|[^ ]+', response.message)

        self.contenttype = ("%s/%s" % (self.type, self.subtype)).lower()

        if self.type == "multipart":
            self.multipart = True

        if self.charset == "-":
            self.charset = None

        if self.encoding == "-":
            self.encoding = None

        self.name = self.name.strip('"')

        self.headerStart = int(self.headerStart)
        self.headerLen = int(self.headerLen)
        self.start = int(self.start)
        self.length = int(self.length)
        self.partHeaderLen = int(self.partHeaderLen)
        self.lines = int(self.lines)

class CommandStream:
    log = logging.getLogger("Bongo.NmapConnection")

    def __init__(self, socket):
        self.stream = socket.makefile()

    def close(self):
        self.stream.close()

    def Read(self, length=-1):
        return self.stream.read(length)

    def Write(self, str, *args):
        self.log.debug("SEND: %s", str % args)
        self.stream.write(str % args)
        self.stream.write("\r\n")
        self.stream.flush()

    def WriteRaw(self, str):
        self.log.debug("SEND: %s", str)
        self.stream.write(str)
        self.stream.flush()

    def GetResponse(self):
        s = self.readline()
        self.log.debug("READ: %s", s)
        return Response(s[0:4], s[5:])

    def readline(self):
        s = self.stream.readline()
        if s[-2:] == "\r\n":
            s = s[:-2]
        elif s[-1:] == "\n":
            s = s[:-1]
        return s

class AddressBookIterator:
    def __init__(self, stream, count):
        self.stream = stream

        self.done = False
        count, other = count.split(' ', 1)
        self.count = int(count)
        self.total = None

    def __iter__(self):
        return self

    def next(self):
        if self.count > 0:
            r = AddressBookItem(self.stream)
            self.count -= 1
        else:
            r = self.stream.GetResponse()
            raise StopIteration()

        return r

    def finish(self):
        for r in self:
            pass
class ResponseIterator:
    def __init__(self, stream):
        self.stream = stream

        self.done = False
        self.count = None
        self.total = None

    def __iter__(self):
        return self

    def next(self):
        r = self.stream.GetResponse()
        if r.code == 1000:
            if re.match("\d+", r.message):
                self.total = int(r.message)
            self.done = True
        elif r.code > 3000:
            raise CommandError(r)
#            raise BongoError(r.message)

        if self.done:
            raise StopIteration()

        return r

    def finish(self):
        for r in self:
            pass

class CalendarInfoIterator:
    def __init__(self, stream, count):
        self.stream = stream

        self.done = False
        self.count = int(count)
        self.total = None

    def __iter__(self):
        return self

    def next(self):
        if self.count > 0:
            r = CalendarInfoItem(self.stream.readline())
            self.count -= 1
        else:
            r = self.stream.GetResponse()
            raise StopIteration()

        return r

    def finish(self):
        for r in self:
            pass

class MessageInfoIterator:
    def __init__(self, stream, count):
        self.stream = stream

        self.done = False
        self.count = int(count)
        self.total = None

    def __iter__(self):
        return self

    def next(self):
        if self.count > 0:
            r = MessageInfoItem(self.stream.readline())
            self.count -= 1
        else:
            r = self.stream.GetResponse()
            raise StopIteration()

        return r

    def finish(self):
        for r in self:
            pass

class ItemIterator(ResponseIterator):
    def __init__(self, stream, propCount=0):
        ResponseIterator.__init__(self, stream)
        self.propCount = propCount

    def next(self):
        item = Item(ResponseIterator.next(self))
        for i in range(self.propCount):
            r = self.stream.GetResponse()
            if r.code == 2001:
                (key, length) = r.message.split(" ", 2)
                item.props[key] = self.stream.Read(int(length))
                # eat the \r\n afterward
                self.stream.Read(2)

        return item
