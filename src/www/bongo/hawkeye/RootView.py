import bongo.dragonfly
import os
import bongo.dragonfly.BongoUtil
from HawkeyeHandler import HawkeyeHandler
import bongo.hawkeye.Auth as Auth

class RootHandler(HawkeyeHandler):
    def NeedsAuth(self, rp):
        if rp.action == "login":
            return False
        else:
            return True

    def memory(self, VmKey):
        '''Return process memory usage in bytes.
        '''
        _scale = {'kB': 1024.0, 'mB': 1024.0*1024.0,
       'KB': 1024.0, 'MB': 1024.0*1024.0}

        try: # get the /proc/<pid>/status pseudo file
            t = open('/proc/meminfo')
            v = [v for v in t.readlines() if v.startswith(VmKey)]
            t.close()

             # convert Vm value to bytes
            if len(v) == 1:
               t = v[0].split()  # e.g. 'VmRSS:  9999  kB'
               if len(t) == 3:  ## and t[0] == VmKey:
                   return float(t[1]) * _scale.get(t[2], 0.0)
        except:
            pass
        return 0.0

    def index_GET(self, req, rp):
        cm = self.memory("MemTotal:")
        used = cm - self.memory("MemFree:")
	used = used / 1024 / 1024
	cm = cm / 1024 / 1024
        self.SetVariable("mem", str(round(used, 2)) + "MB / " + str(round(cm, 2)) + "MB")
        self.SetVariable("breadcrumb", "Desktop")
        self.SetVariable("dsktab", "selecteditem")
        return self.SendTemplate(req, rp, "index.tpl")

    def test_GET(self, req, rp):
        self.SetVariable("helloworld", "Hello World!")
        self.SetVariable("escapeme", "If anything appears after this, I've been escaped. &nbsp;")
        self.SetVariable("greenstyle", "color: green")
        self.SetVariable("somethingtrue", 1)
        self.SetVariable("somethinguntrue", 0)
        self.SetVariable("testlist", [{ "say": "Hello!" }, { "say": "Bonjour!"}])
        return self.SendTemplate(req, rp, "test.tpl")

    def login_GET(self, req, rp):
        return self.SendTemplate(req, rp, "login.tpl")

    def login_POST(self, req, rp):
        if not Auth.Login(req):
            return self.login_GET(req, rp)

        target = rp.tmplUriRoot

        if req.args and len(req.args) > 0:
            target = req.args

        return bongo.dragonfly.BongoUtil.redirect(req, target)

    def logout_GET(self, req, rp):
        if req.session:
            req.session.invalidate()
            req.session = None

        return bongo.dragonfly.BongoUtil.redirect(req, "login")
