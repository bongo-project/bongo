import os
import bongo.dragonfly
from HawkeyeHandler import HawkeyeHandler

class SystemHandler(HawkeyeHandler):

    def index_GET(self, req, rp):
        self.SetVariable("breadcrumb", "System")
        self.SetVariable("systab", "selecteditem")
        self.SetVariable("title", "System")
        return self.SendTemplate(req, rp, "index.tpl")
