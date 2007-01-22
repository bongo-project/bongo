import bongo.dragonfly
import bongo.dragonfly.BongoUtil
from HawkeyeHandler import HawkeyeHandler
import bongo.hawkeye.Auth as Auth

class RootHandler(HawkeyeHandler):
    def NeedsAuth(self, rp):
        return False

    def index_GET(self, req, rp):
        return self.SendTemplate(req, rp, "index.psp")

    def login_GET(self, req, rp):
        return self.SendTemplate(req, rp, "login.psp")

    def login_POST(self, req, rp):
        if not Auth.Login(req):
            return self.login_GET(req, rp)

        target = "/admin"

        if req.args and len(req.args) > 0:
            target = req.args

        req.session.save()
        return bongo.dragonfly.BongoUtil.redirect(req, target)

    def logout_GET(self, req, rp):
        if req.session:
            req.session.invalidate()
            req.session = None

        return bongo.dragonfly.BongoUtil.redirect(req, "login")
