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
from libbongo.bootstrap import msgapi
from bongo.Console import wrap
from bongo.MDB import MDB, MDBError

import CmdUtil
import MdbUtil

class UserAdd(Command):
    log = logging.getLogger("bongo.admintool")

    def __init__(self):
        Command.__init__(self, "user-add", aliases=["ua"],
                         summary=_("Add a Bongo user"))

        self.set_usage(_("%prog %cmd <username> [options]"))

        self.add_option("-p", "--userpass", type="string", help=_("password for the newly created user"))

        # placeholder for attributes, so we can assume there's a hash
        # in options.attributes
        self.add_option("", "--attributes", action="store_const", default={},
                        help=optparse.SUPPRESS_HELP)

        args = CmdUtil.GetAttributeArgs(MDB.C_USER, msgapi.C_USER_SETTINGS)

        for arg, desc in args:
            self.add_option("", "--" + arg, type="string", metavar="VALUE",
                            help=desc, action="callback",
                            callback=CmdUtil.RecordAttributes)

    def run(self, options, args):
        if options.attributes.get("surname") is None:
            self.error(_("Required --surname option not supplied"))

        if len(args) == 0:
            self.error(_("You must specify a username"))

        if len(args) > 1:
            self.error(_("Too many arguments: you can only add one user at a time"))

        user = args[0]

        mdb = MdbUtil.GetMdb(options)
        context, username = MdbUtil.GetContext(user)

        # set attributes from cmdline options
        props = {}
        for key, value in options.attributes.items():
            props[Util.Properties[key]] = value

        try:
            Util.AddBongoUser(mdb, context, username, props, options.userpass)
            print _("Added user: %s") % user
        except MDBError, e:
            print _("Error adding %s: %s") % (user, str(e))

class UserDelete(Command):
    log = logging.getLogger("bongo.admintool")

    def __init__(self):
        Command.__init__(self, "user-delete", aliases=["ud"],
                         summary="Delete a Bongo user")
        self.set_usage(_("%prog %cmd <username>"))

    def run(self, options, args):
        if len(args) == 0:
            self.error(_("No user specified"))

        mdb = MdbUtil.GetMdb(options)

        for user in args:
            context, username = MdbUtil.GetContext(user)
            try:
                Util.RemoveBongoUser(mdb, context, username)
                print _("Removed user: %s") % user
            except MDBError, e:
                print _("Error removing %s: %s") % (user, str(e))

class UserInfo(Command):
    log = logging.getLogger("bongo.admintool")

    def __init__(self):
        Command.__init__(self, "user-info", aliases=["ui"],
                         summary=_("Display information about a Bongo user"))
        self.set_usage(_("%prog %cmd <username>"))

    def run(self, options, args):
        if len(args) == 0:
            self.error(_("No user specified!"))

        mdb = MdbUtil.GetMdb(options)

        for user in args:
            context, username = MdbUtil.GetContext(user)

            print "[%s]" % username

            attrs = Util.GetUserAttributes(mdb, context, username)

            keys = attrs.keys()
            keys.sort(lambda x,y: cmp(Util.PropertiesPrintable.get(x),
                                      Util.PropertiesPrintable.get(y)))

            for k in keys:
                print " |->(%s)" % Util.PropertiesPrintable[k]
                for val in attrs[k]:
                    print "     |->\"%s\"" % val

class UserList(Command):
    log = logging.getLogger("bongo.admintool")

    def __init__(self):
        Command.__init__(self, "user-list", aliases=["ul"],
                         summary=_("List Bongo users"))
        self.set_usage(_("%prog %cmd"))

    def run(self, options, args):
        mdb = MdbUtil.GetMdb(options)
        context = msgapi.GetConfigProperty(msgapi.DEFAULT_CONTEXT)

        print '[%s]' % context
        for user in Util.ListBongoUsers(mdb, context):
            print ' |->[%s]' % user

class UserModify(Command):
    log = logging.getLogger("bongo.admintool")

    def __init__(self):
        Command.__init__(self, "user-modify", aliases=["um"],
                         summary=_("Modify a Bongo user"))

        self.set_usage(_("%prog %cmd <username> [options]"))

        # placeholder for attributes, so we can assume there's a hash
        # in options.attributes
        self.add_option("", "--attributes", action="store_const", default={},
                        help=optparse.SUPPRESS_HELP)

        args = CmdUtil.GetAttributeArgs(MDB.C_USER, msgapi.C_USER_SETTINGS)

        for arg, desc in args:
            self.add_option("", "--" + arg, type="string", metavar="VALUE",
                            help=desc, action="callback",
                            callback=CmdUtil.RecordAttributes)

    def run(self, options, args):
        if len(args) == 0:
            self.error(_("No user specified!"))

        user = args[0]

        mdb = MdbUtil.GetMdb(options)
        context, username = MdbUtil.GetContext(user)

        # Check that the user object exists.  This throws an error if
        # it doesn't.
        mdb.GetObjectDetails(context + "\\" + username)

        props = {}
        for key, value in options.attributes.items():
            props[Util.Properties[key]] = value

        Util.ModifyBongoUser(mdb, context, username, props)
        print "%s modified" % username
        
class UserPasswd(Command):
    log = logging.getLogger("bongo.admintool")

    def __init__(self):
        Command.__init__(self, "user-passwd", aliases=["up"],
                         summary=_("Change a Bongo user's password"))
        self.set_usage(_("%prog %cmd <username>"))

    def run(self, options, args):
        if len(args) == 0:
            self.error(_("No user specified!"))

        if len(args) > 2:
            self.error(_("Too many arguments specified"))

        if len(args) == 1:
            user = args[0]
            print _("Changing password for %s.") % user
            passwd1 = getpass.getpass(_("New Password: "))
            passwd2 = getpass.getpass(_("Reenter New Password: "))

            if passwd1 != passwd2:
                self.error(_("Passwords do not match."))

            passwd = passwd1
        elif len(args) == 2:
            user, passwd = args

        mdb = MdbUtil.GetMdb(options)
        context, username = MdbUtil.GetContext(user)
        mdb.SetPassword("\\".join((context, username)), passwd)
        print _("Password changed.")
