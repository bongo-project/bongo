#!/usr/bin/env python

import logging, os, pwd, sys
import getpass
from gettext import gettext as _

from bongo import Privs
from bongo.cmdparse import CommandParser, Command

from bongo.admintool import AgentCommands
from bongo.admintool import DemoCommands
from bongo.admintool import ServerCommands
from bongo.admintool import SetupCommands
from bongo.admintool import UserCommands

parser = CommandParser()
parser.add_option("-U", "--authuser", type="string", default="admin",
                  help="admin username [default %default]")
parser.add_option("-P", "--authpass", type="string",
                  help="admin password")
parser.add_option("-c", "--context", type="string",
                  help="MDB context")
parser.add_option("", "--debug", action="store_true",
                  help="enable debugging output")

parser.add_commands(AgentCommands, "Agent")
parser.add_commands(DemoCommands)
parser.add_commands(ServerCommands, "Server")
parser.add_commands(SetupCommands, "Setup")
parser.add_commands(UserCommands, "User")

if __name__ == '__main__':
    (command, options, args) = parser.parse_args()

    formatter = logging.Formatter("%(levelname)s: %(message)s")
    console = logging.StreamHandler()
    console.setFormatter(formatter)
    logging.root.addHandler(console)

    if options.debug:
        logging.root.setLevel(logging.DEBUG)
    else:
        logging.root.setLevel(logging.INFO)

    log = logging.getLogger("bongo.admintool")

    if command is None:
        parser.print_help()
        parser.exit()

    if os.getegid() != 0:
        log.error("%s must be run as root" % os.path.basename(sys.argv[0]))
        sys.exit(1)

    Privs.DropPrivs()

    if options.authpass is None:
        try:
            options.authpass = getpass.getpass(_("Enter password for %s: ") % options.authuser)
        except KeyboardInterrupt:
            print
            sys.exit(1)

    log.debug("running command: %s", str(command))

    # pull the password out of the printed args
    authpass = options.authpass
    options.authpass = "*****"
    log.debug("options: %s", str(options))
    options.authpass = authpass

    if len(args) > 0:
        log.debug("extra args: %s", str(args))

    try:
        ret = command.run(options, args)
    except KeyboardInterrupt:
        print
        sys.exit(1)
    except SystemExit:
        raise
    except Exception, e:
        log.debug("Error at bongo-admintool main()", exc_info=1)
        ret = 1
        print _("ERROR: %s") % str(e)

    if ret is None:
        sys.exit(0)

    sys.exit(ret)
