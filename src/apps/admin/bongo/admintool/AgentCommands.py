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
from bongo.MDB import MDB, MDBError

import CmdUtil
import MdbUtil

class AgentDelete(Command):
    log = logging.getLogger("bongo.admintool")

    def __init__(self):
        Command.__init__(self, "agent-delete", aliases=["ad"],
                         summary="Delete a Bongo agent")
        self.set_usage(_("%prog %cmd <agent>"))
        self.add_option("", "--server", action="store_const", default="Bongo Messaging Server")

    def run(self, options, args):
        if len(args) == 0:
            self.error(_("No agent specified"))

        mdb = MdbUtil.GetMdb(options)

        for agent in args:
            try:
                Util.RemoveBongoAgent(mdb, options.server, agent)
                print _("Removed agent: %s") % agent
            except MDBError, e:
                self.log.debug("Error removing agent %s", agent, exc_info=1)
                print _("Error removing %s: %s") % (agent, str(e))

class AgentInfo(Command):
    log = logging.getLogger("bongo.admintool")

    def __init__(self):
        Command.__init__(self, "agent-info", aliases=["ai"],
                         summary=_("List info for an agent"))
        self.set_usage(_("%prog %cmd <agent>"))
        self.add_option("", "--server", type="string", default="Bongo Messaging Server", help="[default %default]")

    def run(self, options, args):
        if len(args) == 0:
            self.error(_("No agent specified!"))

        mdb = MdbUtil.GetMdb(options)

        print "[%s]" % options.server

        for agent in args:
            dn = options.server + "\\" + agent
            attrs = Util.GetAgentAttributes(mdb, dn)

            keys = attrs.keys()
            keys.sort(lambda x,y: cmp(Util.PropertiesPrintable.get(x),
                                      Util.PropertiesPrintable.get(y)))

            for k in keys:
                print " |->(%s)" % Util.PropertiesPrintable[k]
                for val in attrs[k]:
                    print " |   |->\"%s\"" % val

class AgentList(Command):
    log = logging.getLogger("bongo.admintool")

    def __init__(self):
        Command.__init__(self, "agent-list", aliases=["al"],
                         summary=_("List configured Bongo agents"))
        self.set_usage(_("%prog %cmd"))

    def run(self, options, args):
        mdb = MdbUtil.GetMdb(options)

        for server in Util.ListBongoMessagingServers(mdb):
            agents = Util.ListBongoAgents(mdb, server)
            print 'Installed agents:'
            print '[%s]' % server
            for agent in agents:
                print ' |->[%s]' % agent

class AgentModify(Command):
    log = logging.getLogger("bongo.admintool")

    def __init__(self):
        Command.__init__(self, "agent-modify", aliases=["am"],
                         summary=_("Modify a Bongo agent"))

        self.set_usage(_("%prog %cmd <agent> [options]"))
        self.add_option("", "--server", type="string", default="Bongo Messaging Server", help="[default %default]")

        # placeholder for attributes, so we can assume there's a hash
        # in options.attributes
        self.add_option("", "--attributes", action="store_const", default={},
                        help=optparse.SUPPRESS_HELP)

        agentArgs = CmdUtil.GetAgentArgs()
        seenArgs = {}

        for agent, args in agentArgs.items():
            group = optparse.OptionGroup(self, self.get_pretty_agent(agent))

            for arg, desc in args:
                if seenArgs.has_key(arg):
                    # you can only add_option a long opt once
                    continue

                group.add_option(
                    "", "--" + arg, type="string", metavar="VALUE",
                    help=desc, action="callback",
                    callback=CmdUtil.RecordAttributes)
                seenArgs[arg] = 1

            self.add_option_group(group)

    def get_pretty_agent(self, agent):
        name = Util.EntitiesPrintable.get(agent, "Unknown Agent")

        if name == "Agent":
            name = "Common"

        return "%s Attributes" % name

    def run(self, options, args):
        if len(args) == 0:
            self.error(_("No agent specified!"))

        agent = args[0]

        mdb = MdbUtil.GetMdb(options)

        props = {}
        for key, value in options.attributes.items():
            props[Util.Properties[key]] = value

        Util.ModifyBongoAgent(mdb, options.server, agent, props)
        print "%s modified" % agent
