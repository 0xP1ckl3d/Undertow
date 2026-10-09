using System;
using System.Security.Principal;

internal static class TokenIdentity
{
    public static int Main()
    {
        Console.WriteLine(WindowsIdentity.GetCurrent().User.Value);
        return 0;
    }
}
