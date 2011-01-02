#!/usr/bin/env python

import logging, os, pwd, sys

sys.path.insert(0, "${PYTHON_SITEPACKAGES_PATH}")
sys.path.insert(0, "${PYTHON_SITELIB_PATH}")

from bongo.Agent import Agent

class DirectorAgent(Agent):
    log = logging.getLogger("Bongo.Director")
    
    def __init__(self, name="bongo-director"):
        Agent.__init__(self, name)
        pass
    
    def callback(self, response):
        # something needs to be directed...
        pass


director = DirectorAgent()
director.daemonize()

try:
    director.connect("admin", "_system")
except:
    exit(-1)

director.waitForEvents()
exit(0)
