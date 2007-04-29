import bongo.dragonfly
from HawkeyeHandler import HawkeyeHandler

class SummaryHandler(HawkeyeHandler):
    def index_GET(self, req, rp):
        return self.SendTemplate(req, rp, "index.tpl", vars)
