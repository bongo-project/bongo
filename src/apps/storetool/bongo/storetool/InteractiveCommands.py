import logging
import os
import select
import socket
import sys
import telnetlib
import time

try:
    import readline
except ImportError:
    pass

from bongo.cmdparse import Command
from bongo.Contact import Contact
from bongo.store.CommandStream import CommandStream
from bongo.store.StoreConnection import Auth

class BongoTelnet(telnetlib.Telnet):
    """A Telnet class made for talking to Bongo Stores.  Uses readline."""
    def mt_interact(self):
        """Multithreaded version of interact()."""
        import thread
        thread.start_new_thread(self.listener, ())
        while 1:
            try:
                line = raw_input()
            except EOFError:
                return
            if not line:
                break
            self.write("%s\r\n" % line.strip())

    def listener(self):
        """Helper for mt_interact() -- this executes in the other thread."""
        while 1:
            try:
                rfd, wfd, xfd = select.select([self], [], [])
                if self in rfd:
                    try:
                        text = self.read_eager()
                    except EOFError:
                        print '*** Connection closed by remote host ***'
                        break
                    if text:
                        sys.stdout.write(text)
                        sys.stdout.flush()
            except EOFError:
                print '*** Connection closed by remote host ***'
                return
            except:
                return

class TelnetCommand(Command):
    log = logging.getLogger("Bongo.StoreTool")

    def __init__(self):
        Command.__init__(self, "telnet", aliases=["t"],
                         summary="Start an interactive telnet session")

    def Run(self, options, args):
        try:
            print "Trying %s..." % socket.gethostbyname(options.host)
            tn = BongoTelnet(options.host, options.port)
            print "Connected to %s." % options.host

            self.stream = CommandStream(tn.get_socket())
            Auth(self.stream)

            print "USER %s" % options.user
            self.stream.Write("USER %s" % options.user)
            r = self.stream.GetResponse()
            print "%d %s" % (r.code, r.message)

            print "STORE %s" % options.store
            self.stream.Write("STORE %s" % options.store)
            r = self.stream.GetResponse()
            print "%d %s" % (r.code, r.message)

            tn.mt_interact()
        except KeyboardInterrupt:
            pass

        tn.close()
