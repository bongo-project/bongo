import os
import getpass
import logging
import optparse
import shutil
import socket

from gettext import gettext as _

from bongo.cmdparse import Command

from bongo import Privs, Xpl
from bongo.admin import Schema, Util
from bongo.bootstrap import msgapi
from bongo.Console import wrap

import MdbUtil

class ServerInfo(Command):
    log = logging.getLogger("bongo.admintool")

    def __init__(self):
        Command.__init__(self, "server-info", aliases=["si"],
                         summary=_("List information about configured Bongo servers"))
        self.set_usage(_("%prog %cmd [server1 [server2 ...]]"))

    def run(self, options, args):
        mdb = MdbUtil.GetMdb(options)

        if len(args) == 0:
            servers = Util.ListBongoMessagingServers(mdb)
        else:
            servers = args

        print 'Installed servers:'
        for server in servers:
            print '[%s]' % server

            attrs = Util.GetServerAttributes(mdb, server)
            keys = attrs.keys()
            keys.sort(lambda x,y: cmp(Util.PropertiesPrintable.get(x),
                                      Util.PropertiesPrintable.get(y)))

            for key in keys:
                print " |->(%s)" % Util.PropertiesPrintable[key]
                for val in attrs[key]:
                    print " |   |->\"%s\"" % val

class ServerList(Command):
    log = logging.getLogger("bongo.admintool")

    def __init__(self):
        Command.__init__(self, "server-list", aliases=["sl"],
                         summary=_("List configured Bongo servers"))
        self.set_usage(_("%prog %cmd"))

    def run(self, options, args):
        mdb = MdbUtil.GetMdb(options)

        servers = Util.ListBongoMessagingServers(mdb)

        print 'Installed servers:'
        for server in servers:
            print '[%s]' % server
