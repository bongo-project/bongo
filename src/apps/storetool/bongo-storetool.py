#!/usr/bin/env python

import logging, os, pwd, sys

from bongo.cmdparse import CommandParser, Command

from bongo.storetool import BackupCommands
from bongo.storetool import CalendarCommands
from bongo.storetool import ContactCommands
from bongo.storetool import InteractiveCommands
from bongo.storetool import MailCommands
from bongo.storetool import TestCommands

parser = CommandParser()
parser.add_option("", "--host", type="string", default="localhost",
                  help="hostname [default %default]")
parser.add_option("", "--port", type="string", default="689",
                  help="port [default %default]")
parser.add_option("", "--debug", action="store_true",
                  help="enable debugging output")
parser.add_option("-u", "--user", type="string",
                  help="connect as this user")
parser.add_option("-p", "--password", type="string",
                  help="connect with this password")
parser.add_option("-s", "--store", type="string",
                  help="store (if different from user)")

parser.add_commands(BackupCommands, "Backup")
parser.add_commands(CalendarCommands, "Calendar")
parser.add_commands(ContactCommands, "Contact")
parser.add_commands(InteractiveCommands)
parser.add_commands(MailCommands, "Mail")
parser.add_commands(TestCommands, "Testing")

if __name__ == '__main__':
    (command, options, args) = parser.parse_args()

    formatter = logging.Formatter("%(levelname)s: %(message)s")
    console = logging.StreamHandler()
    console.setFormatter(formatter)
    logging.root.addHandler(console)

    if options.debug:
        logging.root.setLevel(logging.DEBUG)
    else:
        logging.root.setLevel(logging.ERROR)

    if command is None:
        parser.print_help()
        parser.exit()

    parser.check_required("-u")

    if options.store is None:
        options.store = options.user

    command.Run(options, args)
