#############################################################################
#
#  Copyright (c) 2006 Novell, Inc.
#  All Rights Reserved.
#
#  This program is free software; you can redistribute it and/or
#  modify it under the terms of version 2.1 of the GNU Lesser General Public
#  License as published by the Free Software Foundation.
#
#  This program is distributed in the hope that it will be useful,
#  but WITHOUT ANY WARRANTY; without even the implied warranty of
#  MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
#  GNU Lesser General Public License for more details.
#
#  You should have received a copy of the GNU Lesser General Public License
#  along with this program; if not, contact Novell, Inc.
#
#  To contact Novell about this file by physical or electronic mail,
#  you may find current contact information at www.novell.com
#
#############################################################################

import logging

# BongoException is the basis for any exceptions in the bongo python
# bindings.  It's automatically logged, otherwise it's boring.

class BongoError(Exception):
    log = logging.getLogger("Bongo.Error")

    def __init__(self, str):
        Exception.__init__(self, str)
        self.log.debug(str)
