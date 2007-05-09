import os
import bongo.dragonfly
from HawkeyeHandler import HawkeyeHandler

class SummaryHandler(HawkeyeHandler):
    _proc_status = '/proc/%d/status' % os.getpid()  # Linux only?
    _scale = {'kB': 1024.0, 'mB': 1024.0*1024.0,
       'KB': 1024.0, 'MB': 1024.0*1024.0}

    def _VmB(VmKey):
        global _scale
        try: # get the /proc/<pid>/status pseudo file
            t = open(_proc_status)
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

    def memory(since=0.0):
        '''Return process memory usage in bytes.
        '''
        return _VmB('VmSize:') - since

    def stacksize(since=0.0):
        '''Return process stack size in bytes.
        '''
        return _VmB('VmStk:') - since 

    def index_GET(self, req, rp):
        self.SetVariable("mem", memory())
        self.SetVariable("breadcrumb", "Desktop")
        self.SetVariable("dsktab", "selecteditem")
        return self.SendTemplate(req, rp, "index.tpl", vars)
