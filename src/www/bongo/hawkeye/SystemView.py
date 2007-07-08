import os
import bongo.dragonfly
from HawkeyeHandler import HawkeyeHandler

class SystemHandler(HawkeyeHandler):

    def index_GET(self, req, rp):
        self.SetVariable("systab", "selecteditem")
        return self.SendTemplate(req, rp, "index.tpl")
