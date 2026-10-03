#include "beacon.h"
void go(char *args, int length) {
    datap parser;
    formatp format;
    int size, count;
    char *formatted;
    int integer;
    short small;
    char *ansi, *wide, *binary;
    BeaconDataParse(&parser, args, length);
    integer = BeaconDataInt(&parser);
    small = BeaconDataShort(&parser);
    ansi = BeaconDataExtract(&parser, &size);
    wide = BeaconDataExtract(&parser, &size);
    binary = BeaconDataExtract(&parser, &count);
    if (!ansi || !wide || !binary) { BeaconPrintf(13, "bad argument buffer\n"); return; }
    BeaconFormatAlloc(&format, 256);
    BeaconFormatPrintf(&format, "int=%d short=%d ansi=%s wide0=%04x binary=%d remain=%d", integer, small, ansi, (unsigned int)*(wchar_t *)wide, count, BeaconDataLength(&parser));
    formatted = BeaconFormatToString(&format, &size);
    BeaconOutput(0, formatted, size);
    BeaconFormatReset(&format);
    BeaconFormatAppend(&format, "reset", 5);
    formatted = BeaconFormatToString(&format, &size);
    BeaconOutput(0, formatted, size);
    BeaconFormatInt(&format, 123);
    BeaconFormatFree(&format);
}
