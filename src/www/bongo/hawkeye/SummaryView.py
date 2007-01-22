import bongo.dragonfly
from HawkeyeHandler import HawkeyeHandler

class SummaryHandler(HawkeyeHandler):
    def index_GET(self, req, rp):
        mdb = self.GetMdb(req)
        vars = {"mdb" : mdb}
        return self.SendTemplate(req, rp, "index.psp", vars)
