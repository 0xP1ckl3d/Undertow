#include "beacon.h"

__declspec(dllimport) void MissingPlainImport(void);
void BeaconUnsupportedHelper(void);
void OtherUnknownExternal(void);

void go(char *args, int length) {
    (void)args; (void)length;
    MissingPlainImport();
    BeaconUnsupportedHelper();
    OtherUnknownExternal();
}
